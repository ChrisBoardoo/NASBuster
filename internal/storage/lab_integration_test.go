// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build integration && linux

// Integration tests against the real kernel, run as root in the cwnsb-lab WSL
// distro only (ADR-0006). They create loop devices backed by files under
// /var/lib/cwnsb-lab, and every destructive command first re-checks that its
// target is one of those loops.
//
//	GOOS=linux GOARCH=amd64 go test -c -tags integration -o bin/storage.integration.test ./internal/storage/
//	wsl -d cwnsb-lab -- env CWNSB_LAB=1 /mnt/h/Work/repos/NASBuster/bin/storage.integration.test -test.v
package storage_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/crosswax/nasbuster/internal/storage"
	"github.com/crosswax/nasbuster/internal/sysexec"
)

const labDir = "/var/lib/cwnsb-lab"

type lab struct {
	t     *testing.T
	run   sysexec.OSRunner
	dir   string
	loops map[string]string // /dev/loopN -> backing file
}

func newLab(t *testing.T) *lab {
	t.Helper()
	host, _ := os.Hostname()
	disposable := host == "cwnsb-lab" || os.Getenv("GITHUB_ACTIONS") == "true"
	if os.Getenv("CWNSB_LAB") != "1" || !disposable || os.Geteuid() != 0 {
		t.Skip("integration tests run as root with CWNSB_LAB=1, in the cwnsb-lab distro or a CI runner")
	}
	dir, err := os.MkdirTemp(labDir, "it-")
	if err != nil {
		t.Fatal(err)
	}
	l := &lab{t: t, run: sysexec.OSRunner{Timeout: time.Minute}, dir: dir, loops: map[string]string{}}
	t.Cleanup(l.cleanup)
	return l
}

func (l *lab) exec(name string, args ...string) string {
	l.t.Helper()
	res, err := l.run.Run(context.Background(), name, args...)
	if err != nil || res.ExitCode != 0 {
		l.t.Fatalf("%s %v: exit %d, err %v, stderr %s", name, args, res.ExitCode, err, res.Stderr)
	}
	return strings.TrimSpace(string(res.Stdout))
}

// addLoop creates a sparse image and attaches it.
func (l *lab) addLoop(name string, size int64) string {
	l.t.Helper()
	file := filepath.Join(l.dir, name+".img")
	f, err := os.Create(file)
	if err != nil {
		l.t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		l.t.Fatal(err)
	}
	f.Close()
	dev := l.exec("losetup", "--find", "--show", file)
	l.loops[dev] = file
	return dev
}

// mustBeOurs refuses to touch any device that is not one of this test's loops.
func (l *lab) mustBeOurs(dev string) {
	l.t.Helper()
	file, ok := l.loops[dev]
	backing, err := os.ReadFile("/sys/block/" + strings.TrimPrefix(dev, "/dev/") + "/loop/backing_file")
	if !ok || err != nil || strings.TrimSpace(string(backing)) != file {
		l.t.Fatalf("SAFETY: refusing to touch %s (not a loop created by this test)", dev)
	}
}

func (l *lab) cleanup() {
	ctx := context.Background()
	_, _ = l.run.Run(ctx, "mdadm", "--stop", "/dev/md/cwnsb-it")
	for dev := range l.loops {
		_, _ = l.run.Run(ctx, "losetup", "-d", dev)
	}
	_ = os.RemoveAll(l.dir)
}

func (l *lab) discover() map[string]storage.Disk {
	l.t.Helper()
	d := &storage.Discoverer{Run: l.run, FS: os.DirFS("/"), LabDir: labDir}
	disks, err := d.Disks(context.Background())
	if err != nil {
		l.t.Fatal(err)
	}
	out := map[string]storage.Disk{}
	for _, disk := range disks {
		out[disk.DevPath] = disk
	}
	return out
}

func TestLabDiscoveryRolesOnRealKernel(t *testing.T) {
	l := newLab(t)
	a := l.addLoop("a", 512<<20)
	b := l.addLoop("b", 512<<20)
	c := l.addLoop("c", 512<<20)

	disks := l.discover()
	for _, dev := range []string{a, b, c} {
		d, ok := disks[dev]
		if !ok || d.Role != storage.RoleAvailable || d.Transport != "loop" || !strings.HasPrefix(d.ID, storage.LabIDPrefix) {
			t.Fatalf("%s = %+v", dev, d)
		}
	}
	for dev, d := range disks {
		if _, ours := l.loops[dev]; !ours && d.Role == storage.RoleAvailable && !strings.HasPrefix(dev, "/dev/loop") {
			t.Errorf("SAFETY: non-lab disk %s reported available", dev)
		}
		if !strings.HasPrefix(dev, "/dev/loop") && d.Role != storage.RoleProtected {
			t.Errorf("non-lab disk %s role = %s, want protected", dev, d.Role)
		}
	}

	// A Btrfs filesystem makes the disk in_use.
	l.mustBeOurs(a)
	l.exec("mkfs.btrfs", "-f", "-L", "cwnsb-it", a)
	// A RAID1 array makes both members in_use.
	l.mustBeOurs(b)
	l.mustBeOurs(c)
	l.exec("mdadm", "--create", "/dev/md/cwnsb-it", "--run", "--level=1", "--raid-devices=2",
		"--metadata=1.2", "--bitmap=internal", "--homehost=any", b, c)
	l.exec("udevadm", "settle")

	disks = l.discover()
	if d := disks[a]; d.Role != storage.RoleInUse || !hasUsage(d, "btrfs_member") {
		t.Errorf("btrfs loop = %s %+v", d.Role, d.Usage)
	}
	for _, dev := range []string{b, c} {
		if d := disks[dev]; d.Role != storage.RoleInUse || !hasUsage(d, "md_member") {
			t.Errorf("md member %s = %s %+v", dev, d.Role, d.Usage)
		}
	}
}

func hasUsage(d storage.Disk, kind string) bool {
	return slices.ContainsFunc(d.Usage, func(u storage.Usage) bool { return u.Kind == kind })
}
