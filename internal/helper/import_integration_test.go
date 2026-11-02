// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build integration && linux

// Real-kernel test of pool import after the loss of the system (dead SD
// card), in the cwnsb-lab distro only (see pool_integration_test.go).
package helper

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/crosswax/nasbuster/internal/storage"
)

func TestLabPoolImportAfterSystemLoss(t *testing.T) {
	ctx := context.Background()
	l := newLabPool(t, 5)
	mirror, mres, err := l.create(storage.LayoutMirror, 0, 1)
	if err != nil {
		t.Fatalf("create mirror: %v", err)
	}
	parity, pres, err := l.create(storage.LayoutParity1, 2, 3, 4)
	if err != nil {
		t.Fatalf("create parity: %v", err)
	}
	for _, mnt := range []string{mres.MountPoint, pres.MountPoint} {
		if err := os.WriteFile(filepath.Join(mnt, "keep.txt"), []byte("survives the SD card"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	l.cmd("sync")

	// The SD card dies: a fresh system knows nothing about these pools.
	for _, name := range []string{mirror, parity} {
		l.cmd("systemctl", "disable", "--now", storage.MountUnitName(name))
		os.Remove(filepath.Join(l.ops.UnitDir, storage.MountUnitName(name)))
		os.Remove(storage.MountPoint(name))
		(&poolBuild{o: l.ops, name: name}).removeMDConf()
	}
	l.cmd("systemctl", "daemon-reload")
	l.cmd("mdadm", "--stop", storage.MDDevice(parity))
	// udev on the new system assembles the array under the old host's name.
	l.cmd("mdadm", "--assemble", "--scan", "--homehost=othernas", "--config=/dev/null")
	l.cmd("udevadm", "settle")
	// Debian's mdadm package, installed with NasBuster on the fresh system,
	// describes every array it finds (mkconf: mdadm --examine --scan): two
	// lines for one array after the import, and mdadm assembles nothing.
	debian := l.debianLines(parity)

	scan, err := l.ops.poolsScan(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	found := map[string]ImportCandidate{}
	for _, c := range scan.Pools {
		found[c.Name] = c
	}
	if c := found[mirror]; c.Kind != "btrfs" || !c.Complete || c.Expected != 2 {
		t.Fatalf("mirror candidate = %+v (scan %+v)", c, scan.Pools)
	}
	if c := found[parity]; c.Kind != "md" || !c.Complete || c.Expected != 3 || c.Layout != storage.LayoutParity1 {
		t.Fatalf("parity candidate = %+v", c)
	}

	for name, want := range map[string]string{mirror: storage.LayoutMirror, parity: storage.LayoutParity1} {
		res, err := l.ops.poolImport(ctx, PoolConfirmArgs{Name: name, Confirm: name})
		if err != nil {
			t.Fatalf("import %s: %v", name, err)
		}
		if res.Layout != want || len(res.Members) == 0 {
			t.Fatalf("import %s = %+v", name, res)
		}
		b, rerr := os.ReadFile(filepath.Join(storage.MountPoint(name), "keep.txt"))
		if rerr != nil || string(b) != "survives the SD card" {
			t.Fatalf("data of %s: %q %v", name, b, rerr)
		}
	}
	if _, err := os.Stat(storage.MDDevice(parity)); err != nil {
		t.Fatalf("array not under its CWNSB name: %v", err)
	}
	b, _ := os.ReadFile(l.ops.MDConf)
	if !strings.Contains(string(b), "pool "+parity+"\n") {
		t.Fatal("mdadm.conf block not written")
	}
	for _, line := range debian {
		if !strings.Contains(string(b), mdConfDisabledPrefix+parity+": "+line+"\n") {
			t.Fatalf("Debian's line %q not commented out: %q", line, b)
		}
	}

	// The next boot: the initramfs assembles the array from mdadm.conf alone.
	l.cmd("systemctl", "stop", storage.MountUnitName(parity))
	l.cmd("mdadm", "--stop", storage.MDDevice(parity))
	l.run.Run(ctx, "mdadm", "--assemble", "--scan", "--config="+l.ops.MDConf)
	l.cmd("udevadm", "settle")
	if _, err := os.Stat(storage.MDDevice(parity)); err != nil {
		t.Fatalf("array not assembled from mdadm.conf: %v", err)
	}
	l.cmd("systemctl", "start", storage.MountUnitName(parity))

	if scan, _ := l.ops.poolsScan(ctx); len(scan.Pools) != 0 {
		t.Fatalf("still importable after import: %+v", scan.Pools)
	}
}

// debianLines appends to mdadm.conf what Debian's mkconf writes for a pool's
// array, and takes it out again, commented out or not, after the test.
func (l *labPool) debianLines(name string) []string {
	l.t.Helper()
	res, err := l.run.Run(context.Background(), "mdadm", "--examine", "--scan", "--config=partitions")
	if err != nil || res.ExitCode != 0 {
		l.t.Fatalf("mdadm --examine --scan: %v %s", err, res.Stderr)
	}
	var lines []string
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		if strings.Contains(line, "cwnsb-"+name+" ") {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		l.t.Fatalf("no line for %s in %q", name, res.Stdout)
	}
	conf, _ := os.ReadFile(l.ops.MDConf)
	if err := os.WriteFile(l.ops.MDConf, []byte(string(conf)+strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		l.t.Fatal(err)
	}
	l.t.Cleanup(func() {
		b, err := os.ReadFile(l.ops.MDConf)
		if err != nil {
			return
		}
		kept := strings.Split(string(b), "\n")
		kept = slices.DeleteFunc(kept, func(s string) bool {
			return slices.Contains(lines, strings.TrimPrefix(s, mdConfDisabledPrefix+name+": "))
		})
		os.WriteFile(l.ops.MDConf, []byte(strings.Join(kept, "\n")), 0o644)
	})
	return lines
}
