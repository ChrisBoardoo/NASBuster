// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build integration && linux

// Real-kernel tests of pool creation, run as root in the cwnsb-lab distro (or
// a CI runner) only. Disks are loop devices backed by files under
// /var/lib/cwnsb-lab, and the helper itself runs in lab mode: it cannot see
// any other disk as usable.
//
//	GOOS=linux GOARCH=amd64 go test -c -tags integration -o bin/helper.integration.test ./internal/helper/
//	wsl -d cwnsb-lab -- env CWNSB_LAB=1 /mnt/h/Work/repos/NASBuster/bin/helper.integration.test '-test.v'
package helper

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crosswax/nasbuster/internal/storage"
	"github.com/crosswax/nasbuster/internal/sysexec"
)

const labDir = "/var/lib/cwnsb-lab"

type labPool struct {
	t     *testing.T
	ops   *Ops
	run   sysexec.OSRunner
	dir   string
	loops []string // /dev/loopN
	pools []string
}

func newLabPool(t *testing.T, n int) *labPool {
	t.Helper()
	host, _ := os.Hostname()
	disposable := host == "cwnsb-lab" || os.Getenv("GITHUB_ACTIONS") == "true"
	if os.Getenv("CWNSB_LAB") != "1" || !disposable || os.Geteuid() != 0 {
		t.Skip("integration tests run as root with CWNSB_LAB=1, in the cwnsb-lab distro or a CI runner")
	}
	dir, err := os.MkdirTemp(labDir, "itpool-")
	if err != nil {
		t.Fatal(err)
	}
	l := &labPool{t: t, ops: NewOps(quietLogger(), labDir), run: sysexec.OSRunner{Timeout: 2 * time.Minute}, dir: dir}
	t.Cleanup(l.cleanup)
	for i := 0; i < n; i++ {
		img := filepath.Join(dir, fmt.Sprintf("d%d.img", i))
		f, err := os.Create(img)
		if err != nil {
			t.Fatal(err)
		}
		f.Truncate(640 << 20)
		f.Close()
		res, err := l.run.Run(context.Background(), "losetup", "--find", "--show", "--partscan", img)
		if err != nil || res.ExitCode != 0 {
			t.Fatalf("losetup: %v %s", err, res.Stderr)
		}
		l.loops = append(l.loops, strings.TrimSpace(string(res.Stdout)))
	}
	l.run.Run(context.Background(), "udevadm", "settle")
	return l
}

func (l *labPool) id(i int) string {
	return storage.LabIDPrefix + strings.TrimPrefix(l.loops[i], "/dev/")
}

func (l *labPool) create(layout string, idx ...int) (string, PoolCreateResult, *Error) {
	l.t.Helper()
	name := fmt.Sprintf("it%d", rand.IntN(1_000_000))
	ids := make([]string, len(idx))
	for i, n := range idx {
		ids[i] = l.id(n)
	}
	l.pools = append(l.pools, name)
	res, err := l.ops.poolCreate(context.Background(), PoolCreateArgs{Name: name, Layout: layout, Disks: ids, Confirm: name})
	return name, res, err
}

func (l *labPool) cleanup() {
	ctx := context.Background()
	for _, name := range l.pools {
		l.ops.stopPoolJobs(ctx, name)
		unit := storage.MountUnitName(name)
		l.run.Run(ctx, "systemctl", "disable", "--now", unit)
		os.Remove(filepath.Join(l.ops.UnitDir, unit))
		os.Remove(storage.MountPoint(name))
		(&poolBuild{o: l.ops, name: name}).removeMDConf()
		l.run.Run(ctx, "mdadm", "--stop", storage.MDDevice(name))
	}
	l.run.Run(ctx, "systemctl", "daemon-reload")
	for _, dev := range l.loops {
		if dev == "" {
			continue // unplugged by the test
		}
		l.run.Run(ctx, "wipefs", "-a", dev+"p1")
		l.run.Run(ctx, "losetup", "-d", dev)
	}
	os.RemoveAll(l.dir)
}

func TestLabPoolMirrorLifecycle(t *testing.T) {
	l := newLabPool(t, 3)
	name, res, err := l.create(storage.LayoutMirror, 0, 1)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Logf("created %s uuid=%s", name, res.UUID)

	// Real data goes in, the pool is mounted where expected.
	if err := os.WriteFile(filepath.Join(res.MountPoint, "hello.txt"), []byte("CrossWax NasBuster"), 0o644); err != nil {
		t.Fatalf("write to pool: %v", err)
	}
	st, opErr := l.ops.poolStatus(context.Background(), PoolArgs{Name: name})
	if opErr != nil || !st.Mounted || st.Health != storage.HealthOK || len(st.Devices) != 2 || st.UUID != res.UUID {
		t.Fatalf("status = %+v %v", st, opErr)
	}
	raid1 := false
	for _, p := range st.Profiles {
		if p.Type == "Data" && p.Profile == "RAID1" {
			raid1 = true
		}
	}
	if !raid1 {
		t.Fatalf("data profile is not RAID1: %+v", st.Profiles)
	}

	// The members are now in use: a second pool cannot take them.
	if _, _, err := l.create(storage.LayoutMirror, 1, 2); err == nil || err.Code != CodeDiskNotAvailable {
		t.Fatalf("reusing a member: %v", err)
	}

	// Scrub runs and finishes clean.
	if _, opErr := l.ops.poolScrub(context.Background(), PoolArgs{Name: name}); opErr != nil {
		t.Fatalf("scrub: %v", opErr)
	}
	for i := 0; i < 30; i++ {
		st, _ = l.ops.poolStatus(context.Background(), PoolArgs{Name: name})
		if st.Scrub.Status == "finished" {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if st.Scrub.Status != "finished" || st.Scrub.Uncorrectable != 0 {
		t.Fatalf("scrub = %+v", st.Scrub)
	}
}

func TestLabPoolParity(t *testing.T) {
	l := newLabPool(t, 3)
	name, res, err := l.create(storage.LayoutParity1, 0, 1, 2)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	st, opErr := l.ops.poolStatus(context.Background(), PoolArgs{Name: name})
	if opErr != nil || !st.Mounted || st.MD == nil || st.MD.Level != "raid5" || st.MD.Expected != 3 {
		t.Fatalf("status = %+v md=%+v %v", st, st.MD, opErr)
	}
	conf, _ := os.ReadFile(l.ops.MDConf)
	if !strings.Contains(string(conf), "BEGIN CrossWax NasBuster pool "+name) {
		t.Fatalf("mdadm.conf lacks the pool block")
	}
	if res.MDDevice != storage.MDDevice(name) {
		t.Fatalf("md = %s", res.MDDevice)
	}
}

func TestLabPoolRefusesDiskWithData(t *testing.T) {
	l := newLabPool(t, 2)
	// Someone's data: an ext4 filesystem directly on the loop.
	if res, err := l.run.Run(context.Background(), "mkfs.ext4", "-q", "-F", l.loops[1]); err != nil || res.ExitCode != 0 {
		t.Fatalf("mkfs.ext4: %v %s", err, res.Stderr)
	}
	l.run.Run(context.Background(), "udevadm", "settle")
	_, _, err := l.create(storage.LayoutMirror, 0, 1)
	if err == nil || err.Code != CodeDiskNotAvailable {
		t.Fatalf("err = %v, want %s", err, CodeDiskNotAvailable)
	}
	// The ext4 filesystem is intact.
	res, _ := l.run.Run(context.Background(), "blkid", "-p", "-s", "TYPE", "-o", "value", l.loops[1])
	if strings.TrimSpace(string(res.Stdout)) != "ext4" {
		t.Fatalf("data disk was modified: TYPE=%q", res.Stdout)
	}
}
