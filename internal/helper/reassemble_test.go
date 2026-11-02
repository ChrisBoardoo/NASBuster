// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/crosswax/nasbuster/internal/fixture"
	"github.com/crosswax/nasbuster/internal/sysexec"
)

const (
	e2UUID = "30ab94bd:026ffe21:8fd777ae:9d3632d2" // from the Pi 5
	e2Want = "30ab94bd026ffe218fd777ae9d3632d2"
	// md127 runs with sdc1 left out by the forced assembly.
	e2Running = "-- dev/md/cwnsb-e2 -> ../md127 --\n-- proc/mdstat --\n" +
		"Personalities : [raid5]\n" +
		"md127 : active raid5 sda1[0] sdd1[2]\n" +
		"      243063808 blocks super 1.2 level 5, 512k chunk, algorithm 2 [3/2] [U_U]\n" +
		"      bitmap: 1/1 pages [4KB], 65536KB chunk\n\nunused devices: <none>\n"
)

// superblock renders `mdadm --examine` as on the Pi 5.
func superblock(part, uuid string, role int) sysexec.Result {
	return sysexec.Result{Stdout: []byte(fmt.Sprintf(`/dev/%s:
          Magic : a92b4efc
        Version : 1.2
     Array UUID : %s
     Raid Level : raid5
   Raid Devices : 3
         Events : 763
   Device Role : Active device %d
   Array State : AAA ('A' == active, '.' == missing, 'R' == replacing)
`, part, uuid, role))}
}

// The member left out is probed again just before --re-add or --add: a kernel
// name that now points at another disk is never written to.
func TestTakeBackLeftOutProbesFirst(t *testing.T) {
	roles := map[string]int{"sda1": 0, "sdc1": 1, "sdd1": 2}
	for _, tc := range []struct {
		name     string
		examine  sysexec.Result
		reAdd    int // exit code of --re-add
		taken    bool
		commands []string // mdadm --manage actions run
	}{
		{"same member, bitmap", superblock("sdc1", e2UUID, 1), 0, true, []string{"--re-add"}},
		{"same member, full rebuild", superblock("sdc1", e2UUID, 1), 1, true, []string{"--re-add", "--add"}},
		{"another array now", superblock("sdc1", "11111111:22222222:33333333:44444444", 1), 0, false, nil},
		{"another place now", superblock("sdc1", e2UUID, 2), 0, false, nil},
		{"no superblock now", sysexec.Result{ExitCode: 1}, 0, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := sysexec.NewFake().
				Set(tc.examine, "mdadm", "--examine", "/dev/sdc1").
				Set(sysexec.Result{ExitCode: tc.reAdd}, "mdadm", "--manage", "/dev/md/cwnsb-e2", "--re-add", "/dev/sdc1").
				Set(sysexec.Result{}, "mdadm", "--manage", "/dev/md/cwnsb-e2", "--add", "/dev/sdc1")
			o := &Ops{Run: run, FS: fixture.Parse([]byte(e2Running))}
			reAdded, leftOut := o.takeBackLeftOut(context.Background(), "e2", e2Want, []string{"sda1", "sdc1", "sdd1"}, roles)
			if got := slices.Contains(reAdded, "sdc1"); got != tc.taken || slices.Contains(leftOut, "sdc1") == tc.taken {
				t.Fatalf("reAdded %v, leftOut %v", reAdded, leftOut)
			}
			var manage []string
			for _, c := range run.Calls() {
				if len(c) == 5 && c[1] == "--manage" {
					manage = append(manage, c[3])
				}
			}
			if !slices.Equal(manage, tc.commands) {
				t.Fatalf("mdadm --manage %v, want %v", manage, tc.commands)
			}
		})
	}
}

func TestLockPoolOneOperationAtATime(t *testing.T) {
	o := &Ops{}
	unlock, err := o.lockPool("e2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.lockPool("e2"); err == nil || err.Code != CodePoolBusy {
		t.Fatalf("second operation on e2: %v", err)
	}
	other, err := o.lockPool("tank")
	if err != nil {
		t.Fatalf("another pool is free: %v", err)
	}
	other()
	unlock()
	again, err := o.lockPool("e2")
	if err != nil {
		t.Fatalf("after unlock: %v", err)
	}
	again()
	// Through Dispatch: a reassembly while another holds the pool.
	unlock, _ = o.lockPool("e2")
	defer unlock()
	_, derr := o.Dispatch(context.Background(), Request{Op: OpPoolReassemble, Args: []byte(`{"name":"e2","confirm":"e2"}`)})
	if derr == nil || derr.Code != CodePoolBusy {
		t.Fatalf("reassemble during another operation: %v", derr)
	}
}

// sysfs is the truth for the work under way: a repair not yet in
// /proc/mdstat, and one stopped halfway (sync_min kept).
func TestMDStatusReadsSysfs(t *testing.T) {
	const idle = "-- dev/md/cwnsb-e2 -> ../md127 --\n-- proc/mdstat --\n" +
		"md127 : active raid5 sda1[0] sdd1[2] sdc1[1]\n" +
		"      243063808 blocks super 1.2 level 5, 512k chunk, algorithm 2 [3/3] [UUU]\n\nunused devices: <none>\n"
	for _, tc := range []struct {
		action, syncMin string
		sync            string // "" for none
		interrupted     bool
	}{
		{"idle\n", "0\n", "", false},
		{"idle\n", "81264640\n", "", true},
		{"idle\n", "", "", false},
		{"repair\n", "0\n", "repair", false},
		{"recover\n", "0\n", "", false}, // a failed member, no spare: nothing runs (lab)
		{"frozen\n", "0\n", "", false},
	} {
		files := idle + "-- sys/block/md127/md/sync_action --\n" + tc.action
		if tc.syncMin != "" {
			files += "-- sys/block/md127/md/sync_min --\n" + tc.syncMin
		}
		md := (&Ops{FS: fixture.Parse([]byte(files))}).mdStatus("e2")
		if md == nil {
			t.Fatalf("%q: no status", tc.action)
		}
		if (md.Sync == nil) != (tc.sync == "") || (md.Sync != nil && md.Sync.Action != tc.sync) || md.Interrupted != tc.interrupted {
			t.Fatalf("%q sync_min %q: sync %+v, interrupted %v", tc.action, tc.syncMin, md.Sync, md.Interrupted)
		}
	}
}

// A repair always covers the whole array: sync_min and sync_max are set back
// before it starts (Debian's mdcheck leaves sync_min where it stopped).
func TestRepairParityCoversTheWholeArray(t *testing.T) {
	const complete = "-- dev/md/cwnsb-e2 -> ../md127 --\n-- proc/mdstat --\n" +
		"md127 : active raid5 sda1[0] sdd1[2] sdc1[1]\n" +
		"      243063808 blocks super 1.2 level 5, 512k chunk, algorithm 2 [3/3] [UUU]\n\nunused devices: <none>\n" +
		"-- sys/block/md127/md/sync_action --\nidle\n-- sys/block/md127/md/sync_min --\n81264640\n"
	files := NewMemFiles()
	files.WriteFile("/etc/mdadm/mdadm.conf", []byte("# BEGIN CrossWax NasBuster pool e2\nARRAY /dev/md/cwnsb-e2 metadata=1.2 UUID="+e2UUID+"\n# END CrossWax NasBuster pool e2\n"), 0o644)
	run := sysexec.NewFake().Set(sysexec.Result{}, "mdadm", "--action=repair", "/dev/md/cwnsb-e2")
	var order []string
	o := &Ops{Run: run, FS: fixture.Parse([]byte(complete)), Files: files, MDConf: "/etc/mdadm/mdadm.conf",
		WriteSysfs: func(p string, v []byte) error { order = append(order, p+"="+string(v)); return nil }}
	if _, err := o.poolRepairParity(context.Background(), PoolArgs{Name: "e2"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"/sys/block/md127/md/sync_min=0", "/sys/block/md127/md/sync_max=max"}
	if !slices.Equal(order, want) || len(run.Calls()) != 1 {
		t.Fatalf("writes %v, calls %v", order, run.Calls())
	}
	// Without a way to set the window, no partial repair is started.
	o.WriteSysfs = nil
	if _, err := o.poolRepairParity(context.Background(), PoolArgs{Name: "e2"}); err == nil || len(run.Calls()) != 1 {
		t.Fatalf("repair without the window: %v, calls %v", err, run.Calls())
	}
}

// After a reboot on the Pi 5, the initramfs (built before the pool existed)
// assembled the array as /dev/md/rpi5-nas:cwnsb-e2: no /dev/md/cwnsb-e2. The
// array is found by its UUID, and mdadm is given its kernel device.
func TestArrayFoundByUUIDAfterBoot(t *testing.T) {
	const renamed = "-- dev/md/rpi5-nas:cwnsb-e2 -> ../md127 --\n" +
		"-- dev/disk/by-id/md-uuid-" + e2UUID + " -> ../../md127 --\n" +
		"-- dev/md/cwnsb-other -> ../md126 --\n" + // another pool's name: never taken for e2
		"-- proc/mdstat --\n" +
		"md127 : active (auto-read-only) raid5 sdd1[3] sdc1[1] sda1[0]\n" +
		"      243063808 blocks super 1.2 level 5, 512k chunk, algorithm 2 [3/3] [UUU]\n\nunused devices: <none>\n" +
		"-- sys/block/md127/md/sync_action --\nidle\n-- sys/block/md127/md/sync_min --\n0\n"
	files := NewMemFiles()
	files.WriteFile("/etc/mdadm/mdadm.conf", []byte("# BEGIN CrossWax NasBuster pool e2\nARRAY /dev/md/cwnsb-e2 metadata=1.2 UUID="+e2UUID+"\n# END CrossWax NasBuster pool e2\n"), 0o644)
	run := sysexec.NewFake().Set(sysexec.Result{}, "mdadm", "--action=repair", "/dev/md127")
	o := &Ops{Run: run, FS: fixture.Parse([]byte(renamed)), Files: files, MDConf: "/etc/mdadm/mdadm.conf",
		WriteSysfs: func(string, []byte) error { return nil }}
	md := o.mdStatus("e2")
	if md == nil || md.Active != 3 || md.Degraded() {
		t.Fatalf("array not seen after the boot: %+v", md)
	}
	if got := o.mdDevice("e2"); got != "/dev/md127" {
		t.Fatalf("mdDevice = %q", got)
	}
	if _, err := o.poolRepairParity(context.Background(), PoolArgs{Name: "e2"}); err != nil {
		t.Fatalf("repair: %v (calls %v)", err, run.Calls())
	}
	// Without the UUID link (no mdadm.conf block), a name is the only way.
	o.Files = NewMemFiles()
	if k := o.mdKernel("e2"); k != "" {
		t.Fatalf("found %q without its UUID nor its name", k)
	}
}
