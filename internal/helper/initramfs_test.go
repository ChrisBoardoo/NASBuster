// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/crosswax/nasbuster/internal/storage"
	"github.com/crosswax/nasbuster/internal/sysexec"
)

const (
	debianMDConf = "HOMEHOST <system>\nMAILADDR root\n"
	e2Block      = "# BEGIN CrossWax NasBuster pool e2\nARRAY /dev/md/cwnsb-e2 metadata=1.2 UUID=30ab94bd:026ffe21:8fd777ae:9d3632d2\n# END CrossWax NasBuster pool e2\n"
)

// initramfsEnv fakes the rebuild unit: its state is set by the test between
// calls ("" = never started, or gone after a reboot).
type initramfsEnv struct {
	ops     *Ops
	run     *sysexec.Fake
	files   *MemFiles
	unit    string
	journal []string
	// showFails makes systemctl show fail (a busy D-Bus); stops records, for
	// every stop of the unit, whether a pending copy already existed.
	showFails bool
	stops     []bool
}

func newInitramfsEnv(t *testing.T, conf string) *initramfsEnv {
	t.Helper()
	e := &initramfsEnv{run: sysexec.NewFake(), files: NewMemFiles(updateInitramfsBin)}
	e.files.WriteFile("/etc/mdadm/mdadm.conf", []byte(conf), 0o644)
	e.ops = &Ops{Run: e.run, Files: e.files, MDConf: "/etc/mdadm/mdadm.conf", Logger: quietLogger()}
	e.run.Handle("systemd-run", func([]string) (sysexec.Result, error) {
		e.unit = "activating"
		return sysexec.Result{}, nil
	})
	e.run.Handle("systemctl", func(args []string) (sysexec.Result, error) {
		switch {
		case args[0] == "stop":
			e.stops = append(e.stops, e.files.Exists(initramfsPending))
			e.unit = ""
		case args[0] == "show" && e.showFails:
			return sysexec.Result{ExitCode: 1, Stderr: []byte("Failed to get properties: Connection timed out")}, nil
		case args[0] == "show" && e.unit == "":
			return sysexec.Result{Stdout: []byte("LoadState=not-found\nActiveState=inactive\n")}, nil
		case args[0] == "show":
			result := "success"
			if e.unit == "failed" {
				result = "exit-code"
			}
			return sysexec.Result{Stdout: []byte("LoadState=loaded\nActiveState=" + e.unit + "\nResult=" + result + "\nExecMainStatus=0\n")}, nil
		}
		return sysexec.Result{}, nil
	})
	e.run.Handle("journalctl", func([]string) (sysexec.Result, error) {
		return sysexec.Result{Stdout: []byte(strings.Join(e.journal, "\n") + "\n")}, nil
	})
	return e
}

func (e *initramfsEnv) refresh(t *testing.T) InitramfsResult {
	t.Helper()
	res, err := e.ops.initramfsRefresh(context.Background())
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	return res
}

// succeed ends the rebuild as the unit does: ExecStartPost= records it.
func (e *initramfsEnv) succeed() {
	e.unit = "active"
	if b, err := e.files.ReadFile(initramfsPending); err == nil {
		e.files.WriteFile(initramfsStamp, b, 0o600)
		e.files.Remove(initramfsPending)
	}
}

func (e *initramfsEnv) rebuilds() int {
	n := 0
	for _, c := range e.run.Calls() {
		if c[0] == "systemd-run" {
			n++
		}
	}
	return n
}

func TestInitramfsRebuiltOnceAfterMDConfChanged(t *testing.T) {
	e := newInitramfsEnv(t, debianMDConf+e2Block)

	if got := e.refresh(t); got.State != "started" {
		t.Fatalf("first call = %+v", got)
	}
	args := e.run.Calls()[len(e.run.Calls())-1]
	if !slices.Contains(args, "--unit=cwnsb-job-initramfs") || !slices.Contains(args, "ExecStartPost="+initramfsRecord) ||
		!slices.Equal(args[len(args)-2:], []string{updateInitramfsBin, "-u"}) {
		t.Fatalf("systemd-run = %v", args)
	}
	if got := e.refresh(t); got.State != "running" {
		t.Fatalf("while it runs = %+v", got)
	}
	e.succeed()
	if got := e.refresh(t); got.State != "current" {
		t.Fatalf("after it succeeded = %+v", got)
	}
	if stamp, _ := e.files.ReadFile(initramfsStamp); string(stamp) != debianMDConf+e2Block {
		t.Fatalf("stamp = %q", stamp)
	}
	if e.files.Exists(initramfsPending) {
		t.Fatal("pending copy left behind")
	}
	if got := e.refresh(t); got.State != "current" || e.rebuilds() != 1 {
		t.Fatalf("nothing changed: %+v, %d rebuilds", got, e.rebuilds())
	}

	// The pool is destroyed: its block leaves mdadm.conf, the initramfs follows.
	e.files.WriteFile("/etc/mdadm/mdadm.conf", []byte(debianMDConf), 0o644)
	if got := e.refresh(t); got.State != "started" || e.rebuilds() != 2 {
		t.Fatalf("after the block left = %+v, %d rebuilds", got, e.rebuilds())
	}
}

// A system that never had a parity pool is never rebuilt for nothing.
func TestInitramfsLeftAloneWithoutAPool(t *testing.T) {
	e := newInitramfsEnv(t, debianMDConf)
	if got := e.refresh(t); got.State != "current" || e.rebuilds() != 0 {
		t.Fatalf("no pool: %+v, %d rebuilds", got, e.rebuilds())
	}
	e.files.Remove("/etc/mdadm/mdadm.conf")
	if got := e.refresh(t); got.State != "current" || e.rebuilds() != 0 {
		t.Fatalf("no mdadm.conf: %+v, %d rebuilds", got, e.rebuilds())
	}
}

func TestInitramfsUnsupportedWithoutTheTool(t *testing.T) {
	e := newInitramfsEnv(t, debianMDConf+e2Block)
	e.files.Remove(updateInitramfsBin)
	if got := e.refresh(t); got.State != "unsupported" || len(e.run.Calls()) != 0 {
		t.Fatalf("without update-initramfs: %+v, calls %v", got, e.run.Calls())
	}
}

// A failure is reported once, never undone by a loop of retries in the same
// call, and retried at the next one.
func TestInitramfsFailureReportedThenRetried(t *testing.T) {
	e := newInitramfsEnv(t, debianMDConf+e2Block)
	e.refresh(t)
	e.unit = "failed"
	e.journal = []string{"update-initramfs: Generating /boot/initrd.img-6.18.50+rpt-rpi-2712",
		"E: mkinitramfs failure zstd -q -9 -T0 28", "update-initramfs: failed for /boot/initrd.img-6.18.50+rpt-rpi-2712 with 1."}

	got := e.refresh(t)
	if got.State != "failed" || !strings.Contains(got.LastError, "failed for /boot/initrd.img") || e.rebuilds() != 1 {
		t.Fatalf("after a failure = %+v, %d rebuilds", got, e.rebuilds())
	}
	if e.files.Exists(initramfsStamp) {
		t.Fatal("a failed rebuild must not be recorded as done")
	}
	if got := e.refresh(t); got.State != "started" || got.LastError != "" || e.rebuilds() != 2 {
		t.Fatalf("next call = %+v, %d rebuilds", got, e.rebuilds())
	}
}

// The unit records its success itself: a reboot right after it does not
// rebuild again (seen on the Pi 5 before the fix). Without that record (the
// move failed), the helper records a success it sees.
func TestInitramfsSuccessSurvivesAReboot(t *testing.T) {
	e := newInitramfsEnv(t, debianMDConf+e2Block)
	e.refresh(t)
	e.succeed()
	e.unit = "" // rebooted before any other call
	if got := e.refresh(t); got.State != "current" || e.rebuilds() != 1 {
		t.Fatalf("after a reboot = %+v, %d rebuilds", got, e.rebuilds())
	}

	e = newInitramfsEnv(t, debianMDConf+e2Block)
	e.refresh(t)
	e.unit = "active" // succeeded, but the pending copy is still there
	if got := e.refresh(t); got.State != "current" || e.rebuilds() != 1 {
		t.Fatalf("success seen by the helper = %+v, %d rebuilds", got, e.rebuilds())
	}
}

// A reboot during the rebuild forgets the unit: its outcome is unknown, so the
// initramfs is rebuilt again.
func TestInitramfsRebuiltAgainAfterAReboot(t *testing.T) {
	e := newInitramfsEnv(t, debianMDConf+e2Block)
	e.refresh(t)
	e.unit = ""
	if got := e.refresh(t); got.State != "started" || e.rebuilds() != 2 {
		t.Fatalf("after a reboot = %+v, %d rebuilds", got, e.rebuilds())
	}
}

// mdadm.conf changed while the rebuild ran: the copy taken before it is what
// gets recorded, so the next call rebuilds once more.
func TestInitramfsChangeDuringTheRebuildIsCaught(t *testing.T) {
	e := newInitramfsEnv(t, debianMDConf+e2Block)
	e.refresh(t)
	e.files.WriteFile("/etc/mdadm/mdadm.conf", []byte(debianMDConf+e2Block+strings.ReplaceAll(e2Block, "e2", "e3")), 0o644)
	if got := e.refresh(t); got.State != "running" {
		t.Fatalf("while it runs = %+v", got)
	}
	e.succeed()
	if got := e.refresh(t); got.State != "started" || e.rebuilds() != 2 {
		t.Fatalf("after it ended = %+v, %d rebuilds", got, e.rebuilds())
	}
}

// Pools created before the fix carry "spares=1" (Pi 5): dropped from our
// blocks, before the rebuild, and never from the administrator's lines.
func TestInitramfsDropsSparesFromOurBlocks(t *testing.T) {
	own := "ARRAY /dev/md/home metadata=1.2 spares=1 UUID=11111111:22222222:33333333:44444444\n"
	old := strings.Replace(e2Block, "metadata=1.2 UUID", "metadata=1.2 spares=1 UUID", 1)
	e := newInitramfsEnv(t, debianMDConf+own+old)

	if got := e.refresh(t); got.State != "started" {
		t.Fatalf("refresh = %+v", got)
	}
	want := debianMDConf + own + e2Block
	if conf, _ := e.files.ReadFile("/etc/mdadm/mdadm.conf"); string(conf) != want {
		t.Fatalf("mdadm.conf = %q, want %q", conf, want)
	}
	if pending, _ := e.files.ReadFile(initramfsPending); string(pending) != want {
		t.Fatalf("the rebuild must get the fixed file: %q", pending)
	}
}

// Creating a parity pool writes mdadm.conf, and the helper starts the rebuild
// right away (ADR-0033); a mirror pool leaves mdadm.conf, and the initramfs, alone.
func TestPoolCreateStartsTheInitramfsRebuild(t *testing.T) {
	for layout, want := range map[string]int{storage.LayoutParity1: 1, storage.LayoutMirror: 0} {
		e := newPoolEnv(t)
		e.files.WriteFile(updateInitramfsBin, nil, 0o755)
		e.files.WriteFile("/etc/mdadm/mdadm.conf", []byte(debianMDConf), 0o644)
		e.run.Handle("systemd-run", func([]string) (sysexec.Result, error) { return sysexec.Result{}, nil })
		args, _ := json.Marshal(PoolCreateArgs{Name: "tank", Layout: layout, Disks: []string{"wwn-0xa", "wwn-0xb", "wwn-0xc"}, Confirm: "tank"})
		if _, err := e.ops.Dispatch(context.Background(), Request{Op: OpPoolCreate, Args: args}); err != nil {
			t.Fatalf("%s: %v", layout, err)
		}
		if got := len(e.ran("systemd-run")); got != want {
			t.Errorf("%s: %d rebuilds, want %d", layout, got, want)
		}
	}
}

// A refused operation (another one holds the pool) changed nothing: no rebuild.
func TestRefusedPoolOperationLeavesTheInitramfsAlone(t *testing.T) {
	e := newPoolEnv(t)
	e.files.WriteFile(updateInitramfsBin, nil, 0o755)
	e.files.WriteFile("/etc/mdadm/mdadm.conf", []byte(debianMDConf+strings.ReplaceAll(e2Block, "e2", "tank")), 0o644)
	unlock, lerr := e.ops.lockPool("tank")
	if lerr != nil {
		t.Fatal(lerr)
	}
	defer unlock()
	args, _ := json.Marshal(PoolConfirmArgs{Name: "tank", Confirm: "tank"})
	if _, err := e.ops.Dispatch(context.Background(), Request{Op: OpPoolDestroy, Args: args}); err == nil || err.Code != CodePoolBusy {
		t.Fatalf("destroy while the pool is held = %v", err)
	}
	if len(e.ran("systemd-run")) != 0 || len(e.ran("systemctl")) != 0 {
		t.Fatalf("a refusal must not touch the initramfs: %v", e.run.Calls())
	}
}

// A failure is reported, and a change made after it is rebuilt at once
// instead of waiting for the next retry (review of 2026-10-27).
func TestInitramfsFailureThenNewChangeRebuildsAtOnce(t *testing.T) {
	e := newInitramfsEnv(t, debianMDConf+e2Block)
	e.refresh(t)
	e.unit = "failed"
	e.journal = []string{"update-initramfs: failed for /boot/initrd.img-6.18.50+rpt-rpi-2712 with 1."}
	e.files.WriteFile("/etc/mdadm/mdadm.conf", []byte(debianMDConf+e2Block+strings.ReplaceAll(e2Block, "e2", "e3")), 0o644)

	got := e.refresh(t)
	if got.State != "started" || !strings.Contains(got.LastError, "failed for") || e.rebuilds() != 2 {
		t.Fatalf("new change after a failure = %+v, %d rebuilds", got, e.rebuilds())
	}
}

// A probe that fails says nothing: no reboot is assumed, the rebuild under way
// is neither stopped nor forgotten.
func TestInitramfsProbeErrorChangesNothing(t *testing.T) {
	e := newInitramfsEnv(t, debianMDConf+e2Block)
	e.refresh(t)
	e.showFails = true
	if _, err := e.ops.initramfsRefresh(context.Background()); err == nil {
		t.Fatal("a failed probe must be an error")
	}
	if e.unit != "activating" || !e.files.Exists(initramfsPending) || e.rebuilds() != 1 {
		t.Fatalf("after a failed probe: unit %q, pending %v, %d rebuilds", e.unit, e.files.Exists(initramfsPending), e.rebuilds())
	}
}

// The previous run's unit is stopped before the new copy is written: a helper
// killed between the two never leaves an old success next to a new copy.
func TestInitramfsOldUnitClearedBeforeTheCopy(t *testing.T) {
	e := newInitramfsEnv(t, debianMDConf+e2Block)
	e.refresh(t)
	e.succeed()
	e.refresh(t)
	e.files.WriteFile("/etc/mdadm/mdadm.conf", []byte(debianMDConf), 0o644)
	e.stops = nil
	if got := e.refresh(t); got.State != "started" {
		t.Fatalf("refresh = %+v", got)
	}
	if len(e.stops) == 0 || e.stops[0] {
		t.Fatalf("the old unit must go before the copy is written: stops %v", e.stops)
	}
}

// The line Debian's mdadm package writes on a fresh system for an array it
// finds (Pi 5, phase F): two spaces before metadata=.
const debianE2Line = "ARRAY /dev/md/cwnsb-e2  metadata=1.2 UUID=30ab94bd:026ffe21:8fd777ae:9d3632d2\n"

// Two ARRAY lines for one array and mdadm assembles nothing at boot (lab,
// mdadm 4.4): every line of the administrator's that names the array of one
// of our blocks, by UUID or by device name, is commented out; the others stay.
func TestForeignDuplicatesDisabled(t *testing.T) {
	const off = mdConfDisabledPrefix + "e2: "
	const home = "ARRAY /dev/md/home metadata=1.2 UUID=11111111:22222222:33333333:44444444\n"
	for _, c := range []struct{ name, in, want string }{
		{"debian line", debianMDConf + debianE2Line + e2Block, debianMDConf + off + debianE2Line + e2Block},
		{"other device name, upper-case UUID",
			"ARRAY /dev/md127 metadata=1.2 name=rpi5-nas:cwnsb-e2 UUID=30AB94BD:026FFE21:8FD777AE:9D3632D2\n" + e2Block,
			off + "ARRAY /dev/md127 metadata=1.2 name=rpi5-nas:cwnsb-e2 UUID=30AB94BD:026FFE21:8FD777AE:9D3632D2\n" + e2Block},
		{"UUID on a continuation line",
			"ARRAY /dev/md0 metadata=1.2\n   UUID=30ab94bd:026ffe21:8fd777ae:9d3632d2\n" + home + e2Block,
			off + "ARRAY /dev/md0 metadata=1.2\n" + off + "   UUID=30ab94bd:026ffe21:8fd777ae:9d3632d2\n" + home + e2Block},
		{"same device name, other array",
			"ARRAY /dev/md/cwnsb-e2 UUID=55555555:66666666:77777777:88888888\n" + e2Block,
			off + "ARRAY /dev/md/cwnsb-e2 UUID=55555555:66666666:77777777:88888888\n" + e2Block},
		{"keyword shortened, any case",
			"arr /dev/md/old UUID=30ab94bd:026ffe21:8fd777ae:9d3632d2\n" + e2Block,
			off + "arr /dev/md/old UUID=30ab94bd:026ffe21:8fd777ae:9d3632d2\n" + e2Block},
		{"line after our block", debianMDConf + e2Block + debianE2Line, debianMDConf + e2Block + off + debianE2Line},
		{"other arrays stay", debianMDConf + home + e2Block, debianMDConf + home + e2Block},
		{"no block of ours", debianMDConf + debianE2Line, debianMDConf + debianE2Line},
	} {
		got, _ := foreignDuplicatesDisabled(c.in)
		if got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
		if again, pools := foreignDuplicatesDisabled(got); again != got || len(pools) != 0 {
			t.Errorf("%s: a second pass changed the file (%v)", c.name, pools)
		}
	}
}

// The hourly catch-up fixes a system where Debian's line came after the pool
// (mdadm installed or reconfigured later), then rebuilds the initramfs with it.
func TestInitramfsDisablesDuplicatesOfOurArrays(t *testing.T) {
	e := newInitramfsEnv(t, debianMDConf+debianE2Line+e2Block)
	if got := e.refresh(t); got.State != "started" {
		t.Fatalf("refresh = %+v", got)
	}
	want := debianMDConf + mdConfDisabledPrefix + "e2: " + debianE2Line + e2Block
	if conf, _ := e.files.ReadFile("/etc/mdadm/mdadm.conf"); string(conf) != want {
		t.Fatalf("mdadm.conf = %q, want %q", conf, want)
	}
	if pending, _ := e.files.ReadFile(initramfsPending); string(pending) != want {
		t.Fatalf("the rebuild must get the fixed file: %q", pending)
	}
}

// Import on a fresh system: our block goes in, Debian's line goes out; a
// failed import puts the file back as it was.
func TestMDConfBlockReplacesDebianLineUntilRollback(t *testing.T) {
	files := NewMemFiles()
	before := debianMDConf + debianE2Line
	files.WriteFile("/etc/mdadm/mdadm.conf", []byte(before), 0o644)
	x := &poolBuild{o: &Ops{Run: sysexec.NewFake(), Files: files, MDConf: "/etc/mdadm/mdadm.conf", Logger: quietLogger()}, name: "e2"}
	if err := x.addMDConf("ARRAY /dev/md/cwnsb-e2 metadata=1.2 UUID=30ab94bd:026ffe21:8fd777ae:9d3632d2"); err != nil {
		t.Fatal(err)
	}
	want := debianMDConf + mdConfDisabledPrefix + "e2: " + debianE2Line + e2Block
	if conf, _ := files.ReadFile("/etc/mdadm/mdadm.conf"); string(conf) != want {
		t.Fatalf("after the block: %q, want %q", conf, want)
	}
	x.rollback()
	if conf, _ := files.ReadFile("/etc/mdadm/mdadm.conf"); string(conf) != before {
		t.Fatalf("after the rollback: %q, want %q", conf, before)
	}
}
