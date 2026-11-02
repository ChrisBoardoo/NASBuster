// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"slices"
	"testing"

	"github.com/crosswax/nasbuster/internal/sysexec"
)

// The erase removes signatures and the partition table, then zeroes the start
// of the disk (where a pool's partition will begin), and only then says done.
func TestDiskWipeZeroesTheStartOfTheDisk(t *testing.T) {
	e := newPoolEnv(t)
	e.run.Handle("dd", func([]string) (sysexec.Result, error) { return sysexec.Result{}, nil })
	got, err := e.ops.diskWipe(context.Background(), DiskWipeArgs{ID: "wwn-0xdata", Confirm: "wwn-0xdata"})
	if err != nil || !got["wiped"] {
		t.Fatalf("wipe = %v, %v", got, err)
	}
	var steps []string
	for _, c := range e.run.Calls() {
		switch c[0] {
		case "wipefs", "sgdisk", "dd":
			steps = append(steps, c[0])
		}
	}
	if !slices.Equal(steps, []string{"wipefs", "sgdisk", "dd"}) {
		t.Errorf("steps = %v, want wipefs, sgdisk then dd", steps)
	}
	dd := e.ran("dd")
	want := []string{"if=/dev/zero", "of=/dev/disk/by-id/wwn-0xdata", "bs=1M", "count=16", "conv=fsync", "status=none"}
	if len(dd) != 1 || !slices.Equal(dd[0], want) {
		t.Errorf("dd = %v, want %v", dd, want)
	}
}

func TestDiskWipeStopsWhenZeroingFails(t *testing.T) {
	e := newPoolEnv(t)
	e.run.Handle("dd", func([]string) (sysexec.Result, error) {
		return sysexec.Result{ExitCode: 1, Stderr: []byte("dd: error writing: Input/output error")}, nil
	})
	if _, err := e.ops.diskWipe(context.Background(), DiskWipeArgs{ID: "wwn-0xdata", Confirm: "wwn-0xdata"}); err == nil {
		t.Fatal("a failed zeroing must not report the disk as erased")
	}
}

func TestDiskWipeRefusesTheSystemDisk(t *testing.T) {
	e := newPoolEnv(t)
	_, err := e.ops.diskWipe(context.Background(), DiskWipeArgs{ID: "mmc-SD", Confirm: "mmc-SD"})
	if err == nil || err.Code != CodeDiskProtected || e.destructive() != 0 || len(e.ran("dd")) != 0 {
		t.Fatalf("err = %v, destructive = %d", err, e.destructive())
	}
}
