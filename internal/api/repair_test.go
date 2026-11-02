// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/crosswax/nasbuster/internal/helper"
	"github.com/crosswax/nasbuster/internal/storage"
)

// Demo disks: two blank 4 TB (sda, sdd) and one blank 2 TB (sdc).
const (
	demoSDA = "wwn-0x5000c500c1234567"
	demoSDC = "usb-Seagate_ST2000LM015-2E8174_0123456789ABCDEF-0:0"
	demoSDD = "wwn-0x50014ee2b1234567"
)

func (e *env) createPool(name, layout string, disks ...string) {
	e.t.Helper()
	body := map[string]any{"name": name, "layout": layout, "disks": disks, "confirm": name}
	e.do("POST", "/api/v1/storage/pools", body, http.StatusCreated, nil)
}

func (e *env) pool(name string) poolView {
	e.t.Helper()
	var out struct{ Pool poolView }
	e.do("GET", "/api/v1/storage/pools/"+name, nil, http.StatusOK, &out)
	if out.Pool.Status == nil {
		e.t.Fatalf("no status: %+v", out.Pool)
	}
	return out.Pool
}

func (e *env) advance(d time.Duration) {
	now := e.sys.Clock().Add(d)
	e.sys.Clock = func() time.Time { return now }
}

func TestResumeNeedsADiskWaitingToBeTakenBack(t *testing.T) {
	e := newEnv(t)
	e.setup()
	e.createPool("tank", "mirror", demoSDC, demoSDA)

	if c := errCode(t, e, "POST", "/api/v1/storage/pools/tank/resume", map[string]any{"confirm": "tonk"}, http.StatusBadRequest); c != "validation.confirm" {
		t.Fatalf("bad confirm: %s", c)
	}
	// Every disk is in use: nothing to take back, and the pool is not remounted.
	if c := errCode(t, e, "POST", "/api/v1/storage/pools/tank/resume", map[string]any{"confirm": "tank"}, http.StatusConflict); c != "storage.nothing_to_resume" {
		t.Fatalf("healthy pool: %s", c)
	}
	if st := e.pool("tank").Status; st.CanResume || st.ResyncNeeded || st.Health != storage.HealthOK {
		t.Fatalf("status = %+v", st)
	}
}

// The flow measured on the Pi 5 (ADR-0027), in the demo: a disk comes back to a
// pool mounted degraded, stays unused until "Take it back", then the copies
// are checked before the pool is "ok" again.
func TestResumeADiskThatCameBack(t *testing.T) {
	e := newEnv(t)
	e.setup()
	start := time.Now()
	e.sys.Clock = func() time.Time { return start }
	e.createPool("tank", "mirror", demoSDC, demoSDA)
	if err := e.sys.Fault("unplug", demoSDA); err != nil {
		t.Fatal(err)
	}
	e.do("POST", "/api/v1/storage/pools/tank/mount-degraded", map[string]any{"confirm": "tank"}, http.StatusOK, nil)
	if err := e.sys.Fault("replug", demoSDA); err != nil {
		t.Fatal(err)
	}

	st := e.pool("tank").Status
	if !st.CanResume || st.MissingDevices != 1 || !st.ResyncNeeded || st.Health != storage.HealthDegraded {
		t.Fatalf("disk back, unused = %+v", st)
	}
	var res struct {
		Resume helper.PoolResumeResult
		Pool   poolView
	}
	e.do("POST", "/api/v1/storage/pools/tank/resume", map[string]any{"confirm": "tank"}, http.StatusAccepted, &res)
	if st = res.Pool.Status; !res.Resume.Remounted || !res.Resume.ResyncStarted || st.MountedDegraded || st.MissingDevices != 0 || !st.ResyncNeeded {
		t.Fatalf("after resume = %+v %+v", res.Resume, st)
	}

	// The resync job converts the single chunks, then scrubs: then "ok".
	e.advance(2 * time.Minute)
	if st = e.pool("tank").Status; st.ResyncNeeded || st.MixedProfiles || st.Scrub.Status != "finished" || st.Health != storage.HealthOK {
		t.Fatalf("after the resync = %+v", st)
	}
}

func TestScrubStopAndResume(t *testing.T) {
	e := newEnv(t)
	e.setup()
	start := time.Now()
	e.sys.Clock = func() time.Time { return start }
	e.createPool("tank", "mirror", demoSDC, demoSDA)

	if c := errCode(t, e, "POST", "/api/v1/storage/pools/tank/scrub/cancel", nil, http.StatusConflict); c != "storage.scrub_not_running" {
		t.Fatalf("cancel with nothing running: %s", c)
	}
	e.do("POST", "/api/v1/storage/pools/tank/scrub", nil, http.StatusAccepted, nil)
	e.do("POST", "/api/v1/storage/pools/tank/scrub/cancel", nil, http.StatusAccepted, nil)
	if st := e.pool("tank").Status; st.Scrub.Status != "aborted" {
		t.Fatalf("after cancel = %+v", st.Scrub)
	}
	// Stopped by the user: it goes on where it stopped, on request.
	e.do("POST", "/api/v1/storage/pools/tank/scrub/resume", nil, http.StatusAccepted, nil)
	if st := e.pool("tank").Status; st.Scrub.Status != "running" {
		t.Fatalf("after resume = %+v", st.Scrub)
	}
	e.advance(2 * time.Minute)
	if st := e.pool("tank").Status; st.Scrub.Status != "finished" {
		t.Fatalf("later = %+v", st.Scrub)
	}
	if c := errCode(t, e, "POST", "/api/v1/storage/pools/tank/scrub/resume", nil, http.StatusConflict); c != "storage.scrub_not_stopped" {
		t.Fatalf("resume a finished scrub: %s", c)
	}
}

func TestPoolMissingDiskReplaceFlow(t *testing.T) {
	e := newEnv(t)
	e.setup()
	start := time.Now()
	e.sys.Clock = func() time.Time { return start }
	e.createPool("tank", "mirror", demoSDC, demoSDA)

	// sda is pulled out; at the next boot the mirror cannot mount.
	if err := e.sys.Fault("unplug", demoSDA); err != nil {
		t.Fatal(err)
	}
	st := e.pool("tank").Status
	if st.Mounted || st.Health != storage.HealthUnmounted || st.MissingDevices != 1 || !st.CanMountDegraded {
		t.Fatalf("after unplug = %+v", st)
	}
	if len(st.Devices) != 1 || st.Devices[0].SizeBytes == 0 { // the present member, with its size
		t.Fatalf("unmounted devices = %+v", st.Devices)
	}

	// Degraded mount: confirmed by name.
	if c := errCode(t, e, "POST", "/api/v1/storage/pools/tank/mount-degraded", map[string]any{"confirm": "tonk"}, http.StatusBadRequest); c != "validation.confirm" {
		t.Fatalf("bad confirm: %s", c)
	}
	var mounted struct{ Pool poolView }
	e.do("POST", "/api/v1/storage/pools/tank/mount-degraded", map[string]any{"confirm": "tank"}, http.StatusOK, &mounted)
	st = mounted.Pool.Status
	if !st.Mounted || !st.MountedDegraded || st.Health != storage.HealthDegraded || !st.MixedProfiles {
		t.Fatalf("mounted degraded = %+v", st)
	}
	for _, d := range st.Devices {
		if d.Missing && d.SizeBytes == 0 {
			t.Fatalf("missing device without its size: %+v", d)
		}
	}
	if c := errCode(t, e, "POST", "/api/v1/storage/pools/tank/mount-degraded", map[string]any{"confirm": "tank"}, http.StatusConflict); c != "storage.pool_mounted" {
		t.Fatalf("second degraded mount: %s", c)
	}
	if c := errCode(t, e, "POST", "/api/v1/storage/pools/tank/restore-redundancy", nil, http.StatusConflict); c != "storage.disk_missing" {
		t.Fatalf("restore with a disk missing: %s", c)
	}

	// Replace: only a recorded member, only with a blank disk.
	replace := func(old, new string) map[string]any { return map[string]any{"old": old, "new": new, "confirm": "tank"} }
	if c := errCode(t, e, "POST", "/api/v1/storage/pools/tank/replace", replace("wwn-0x5000000000000000", demoSDD), http.StatusConflict); c != "storage.member_not_found" {
		t.Fatalf("unknown member: %s", c)
	}
	if c := errCode(t, e, "POST", "/api/v1/storage/pools/tank/replace", replace(demoSDA, demoSDC), http.StatusConflict); c != "storage.disk_not_available" {
		t.Fatalf("replace with a member: %s", c)
	}
	var started struct {
		Replace struct {
			Method string
			DevID  int
		}
	}
	e.do("POST", "/api/v1/storage/pools/tank/replace", replace(demoSDA, demoSDD), http.StatusAccepted, &started)
	if started.Replace.Method != "btrfs-replace" || started.Replace.DevID != 2 {
		t.Fatalf("replace = %+v", started)
	}
	p := e.pool("tank")
	if !slices.Equal(p.Members, []string{demoSDC, demoSDD}) || p.Status.Replace == nil || p.Status.Replace.State != "running" {
		t.Fatalf("while replacing: members=%v replace=%+v", p.Members, p.Status.Replace)
	}
	if c := errCode(t, e, "POST", "/api/v1/storage/pools/tank/replace", replace(demoSDA, demoSDD), http.StatusConflict); c != "storage.pool_busy" {
		t.Fatalf("second replace while rebuilding: %s", c)
	}

	// The rebuild ends; the single chunks still need a convert balance.
	e.advance(2 * time.Minute)
	st = e.pool("tank").Status
	if st.Replace.State != "finished" || st.MissingDevices != 0 || !st.MixedProfiles || st.Health != storage.HealthDegraded {
		t.Fatalf("after replace = %+v replace=%+v", st, st.Replace)
	}
	var restore struct {
		Restore struct {
			BalanceStarted bool
			Data, Metadata string
		}
	}
	e.do("POST", "/api/v1/storage/pools/tank/restore-redundancy", nil, http.StatusAccepted, &restore)
	if !restore.Restore.BalanceStarted || restore.Restore.Data != "raid1" {
		t.Fatalf("restore = %+v", restore)
	}
	if st = e.pool("tank").Status; st.Balance == nil || st.Balance.State != "running" {
		t.Fatalf("balance = %+v", st.Balance)
	}
	e.advance(time.Minute)
	if st = e.pool("tank").Status; st.Health != storage.HealthOK || st.MixedProfiles || st.Balance != nil {
		t.Fatalf("after restore = %+v", st)
	}

	var audit struct {
		Entries []struct{ Action, Detail string }
	}
	e.do("GET", "/api/v1/audit?limit=10", nil, http.StatusOK, &audit)
	if audit.Entries[0].Action != "pool.restore_redundancy" || audit.Entries[1].Action != "pool.replace" || audit.Entries[1].Detail != demoSDA+" -> "+demoSDD {
		t.Fatalf("audit = %+v", audit.Entries[:3])
	}
}

func TestPoolReplaceTooSmall(t *testing.T) {
	e := newEnv(t)
	e.setup()
	e.createPool("tank", "mirror", demoSDA, demoSDD)
	e.sys.Fault("unplug", demoSDD)
	e.do("POST", "/api/v1/storage/pools/tank/mount-degraded", map[string]any{"confirm": "tank"}, http.StatusOK, nil)
	var res struct{ Error apiError }
	e.do("POST", "/api/v1/storage/pools/tank/replace", map[string]any{"old": demoSDD, "new": demoSDC, "confirm": "tank"}, http.StatusBadRequest, &res)
	if res.Error.Code != "storage.disk_too_small" || res.Error.Params["requiredBytes"] == nil {
		t.Fatalf("2 TB for a 4 TB member = %+v", res.Error)
	}
	if p := e.pool("tank"); !slices.Equal(p.Members, []string{demoSDA, demoSDD}) {
		t.Fatalf("members changed on a refusal: %v", p.Members)
	}
}

func TestPoolClearErrorsNeedsScrub(t *testing.T) {
	e := newEnv(t)
	e.setup()
	e.createPool("tank", "mirror", demoSDA, demoSDD)
	e.sys.Fault("errors", demoSDD)
	if st := e.pool("tank").Status; st.Health != storage.HealthErrors {
		t.Fatalf("health = %s", st.Health)
	}
	if c := errCode(t, e, "POST", "/api/v1/storage/pools/tank/clear-errors", nil, http.StatusConflict); c != "storage.scrub_required" {
		t.Fatalf("clear before scrub: %s", c)
	}
	start := time.Now()
	e.sys.Clock = func() time.Time { return start }
	e.do("POST", "/api/v1/storage/pools/tank/scrub", nil, http.StatusAccepted, nil)
	if c := errCode(t, e, "POST", "/api/v1/storage/pools/tank/clear-errors", nil, http.StatusConflict); c != "storage.scrub_required" {
		t.Fatalf("clear while the scrub runs: %s", c)
	}
	e.advance(2 * time.Minute)
	if st := e.pool("tank").Status; st.Scrub.Corrected == 0 || st.Health != storage.HealthErrors {
		t.Fatalf("after scrub = %+v", st.Scrub)
	}
	e.do("POST", "/api/v1/storage/pools/tank/clear-errors", nil, http.StatusOK, nil)
	if st := e.pool("tank").Status; st.Health != storage.HealthOK {
		t.Fatalf("after clear = %s %+v", st.Health, st.Devices)
	}
}

func TestParityPoolKeepsRunningDegraded(t *testing.T) {
	e := newEnv(t)
	e.setup()
	e.createPool("tank", "parity1", demoSDA, demoSDC, demoSDD)
	e.sys.Fault("unplug", demoSDC)
	st := e.pool("tank").Status
	if !st.Mounted || st.MD == nil || !st.MD.Degraded() || st.Health != storage.HealthDegraded || st.MissingDevices != 1 || st.MD.Faulty == nil {
		t.Fatalf("parity after unplug = %+v md=%+v", st, st.MD)
	}
	// No blank disk left (sdb holds data): the helper refuses.
	sdb := "usb-WDC_WD40EFRX-68N32N0_0123456789ABCDEF-0:0"
	if c := errCode(t, e, "POST", "/api/v1/storage/pools/tank/replace", map[string]any{"old": demoSDC, "new": sdb, "confirm": "tank"}, http.StatusConflict); c != "storage.disk_not_available" {
		t.Fatalf("replace with a disk holding data: %s", c)
	}
}

// A parity member that came back (ADR-0030): md does not take it back by
// itself; "Take it back" re-adds it without unmounting anything.
func TestParityMemberTakenBack(t *testing.T) {
	e := newEnv(t)
	e.setup()
	start := time.Now()
	e.sys.Clock = func() time.Time { return start }
	e.createPool("tank", "parity1", demoSDA, demoSDC, demoSDD)
	e.sys.Fault("unplug", demoSDC)
	if st := e.pool("tank").Status; st.CanResume {
		t.Fatalf("nothing came back yet = %+v", st)
	}
	e.sys.Fault("replug", demoSDC)
	st := e.pool("tank").Status
	if !st.CanResume || !st.MD.Degraded() || !st.ResyncNeeded || st.Health != storage.HealthDegraded {
		t.Fatalf("member back, outside the array = %+v md=%+v", st, st.MD)
	}
	var res struct {
		Resume helper.PoolResumeResult
		Pool   poolView
	}
	e.do("POST", "/api/v1/storage/pools/tank/resume", map[string]any{"confirm": "tank"}, http.StatusAccepted, &res)
	if st = res.Pool.Status; res.Resume.Remounted || !res.Resume.ResyncStarted || !st.Mounted || st.MD.Degraded() || st.CanResume || !st.ResyncNeeded {
		t.Fatalf("after resume = %+v %+v md=%+v", res.Resume, st, st.MD)
	}
	// The scrub that follows checks the data: then "ok".
	e.do("POST", "/api/v1/storage/pools/tank/scrub", nil, http.StatusAccepted, nil)
	e.advance(2 * time.Minute)
	if st = e.pool("tank").Status; st.ResyncNeeded || st.Health != storage.HealthOK {
		t.Fatalf("after the scrub = %+v", st)
	}
}

// A hub cut took 2 of the 3 members of a parity pool (ADR-0032, the Pi 5 on
// 2026-10-26): out of service until they are all back, never taken back one by
// one; then one forced assembly, a parity repair, and the scrub that follows.
func TestParityReassembleAfterAHubCut(t *testing.T) {
	e := newEnv(t)
	e.setup()
	start := time.Now()
	e.sys.Clock = func() time.Time { return start }
	e.createPool("tank", "parity1", demoSDA, demoSDC, demoSDD)
	e.sys.Fault("unplug", demoSDC)
	e.sys.Fault("unplug", demoSDD)
	st := e.pool("tank").Status
	if st.MD == nil || st.MD.State != "broken" || st.CanResume || st.CanReassemble || st.Reassemble == nil ||
		st.Reassemble.Reason != storage.ReassembleMissingDisks || len(st.Reassemble.Members) != 1 {
		t.Fatalf("2 of 3 gone = %+v md=%+v r=%+v", st, st.MD, st.Reassemble)
	}
	// Owed from the moment it is seen broken, whatever brings it back.
	if !st.RepairNeeded || !st.ResyncNeeded {
		t.Fatalf("broken, nothing owed = %+v", st)
	}
	if c := errCode(t, e, "POST", "/api/v1/storage/pools/tank/reassemble", map[string]any{"confirm": "tank"}, http.StatusConflict); c != "storage.cannot_reassemble" {
		t.Fatalf("reassemble with disks missing: %s", c)
	}
	// A new disk would only become a spare of a broken array.
	sdb := "usb-WDC_WD40EFRX-68N32N0_0123456789ABCDEF-0:0"
	if c := errCode(t, e, "POST", "/api/v1/storage/pools/tank/replace", map[string]any{"old": demoSDC, "new": sdb, "confirm": "tank"}, http.StatusConflict); c != "storage.pool_broken" {
		t.Fatalf("replace in a broken array: %s", c)
	}

	e.sys.Fault("replug", demoSDC)
	if st = e.pool("tank").Status; st.CanReassemble || st.CanResume || st.Reassemble.Reason != storage.ReassembleMissingDisks {
		t.Fatalf("1 of 2 back = %+v r=%+v", st, st.Reassemble)
	}
	if c := errCode(t, e, "POST", "/api/v1/storage/pools/tank/resume", map[string]any{"confirm": "tank"}, http.StatusConflict); c != "storage.pool_broken" {
		t.Fatalf("take back one by one: %s", c)
	}
	e.sys.Fault("replug", demoSDD)
	st = e.pool("tank").Status
	if r := st.Reassemble; !st.CanReassemble || st.CanResume || r == nil || r.Reason != "" || r.Fresh != 1 || r.EventsGap != 6 || len(r.Members) != 3 {
		t.Fatalf("all back = %+v r=%+v", st, st.Reassemble)
	}
	if c := errCode(t, e, "POST", "/api/v1/storage/pools/tank/reassemble", map[string]any{"confirm": "tonk"}, http.StatusBadRequest); c != "validation.confirm" {
		t.Fatalf("wrong confirmation: %s", c)
	}

	var res struct {
		Reassemble helper.PoolReassembleResult
		Pool       poolView
	}
	e.do("POST", "/api/v1/storage/pools/tank/reassemble", map[string]any{"confirm": "tank"}, http.StatusAccepted, &res)
	// mdadm forced what the array needed to start; the other member came back from the bitmap.
	if r := res.Reassemble; len(r.Members) != 3 || len(r.ReAdded) != 1 || len(r.LeftOut) != 0 || !r.Mounted {
		t.Fatalf("reassemble = %+v", r)
	}
	// The parity repair is owed (the scheduler starts it), then the scrub.
	st = res.Pool.Status
	if !st.Mounted || st.MD.Degraded() || st.CanReassemble || st.Reassemble != nil || !st.RepairNeeded || !st.ResyncNeeded || st.Health != storage.HealthDegraded {
		t.Fatalf("after reassemble = %+v md=%+v", st, st.MD)
	}
	if c := errCode(t, e, "POST", "/api/v1/storage/pools/tank/reassemble", map[string]any{"confirm": "tank"}, http.StatusConflict); c != "storage.cannot_reassemble" {
		t.Fatalf("reassemble a running array: %s", c)
	}
	// A scrub started by hand before the repair does not make the pool healthy.
	e.do("POST", "/api/v1/storage/pools/tank/scrub", nil, http.StatusAccepted, nil)
	e.advance(2 * time.Minute)
	if st = e.pool("tank").Status; !st.ResyncNeeded || !st.RepairNeeded || st.Health != storage.HealthDegraded {
		t.Fatalf("scrub before the repair = %+v", st)
	}
}

func TestDemoFaultRoute(t *testing.T) {
	e := newEnv(t) // Deps.DemoFault is nil here: the route must not exist
	e.setup()
	if c := errCode(t, e, "POST", "/api/v1/demo/faults", map[string]any{"kind": "unplug", "disk": demoSDA}, http.StatusNotFound); c != "request.not_found" {
		t.Fatalf("demo route outside demo mode: %s", c)
	}
}

func TestWipeDisk(t *testing.T) {
	e := newEnv(t)
	e.setup()
	start := time.Now()
	e.sys.Clock = func() time.Time { return start }
	role := func(id string) string {
		var disks struct{ Disks []storage.Disk }
		e.do("GET", "/api/v1/storage/disks", nil, http.StatusOK, &disks)
		for _, d := range disks.Disks {
			if d.ID == id {
				return d.Role
			}
		}
		return "absent"
	}
	wipe := func(id string) string {
		return "/api/v1/storage/disks/" + id + "/wipe"
	}

	// sdb is the emulated OMV disk, mounted under /srv/dev-disk-by-uuid-…
	const sdb = "usb-WDC_WD40EFRX-68N32N0_0123456789ABCDEF-0:0"
	if c := errCode(t, e, "POST", wipe(sdb), map[string]any{"confirm": sdb}, http.StatusConflict); c != "storage.disk_mounted" {
		t.Fatalf("mounted disk: %s", c)
	}
	if c := errCode(t, e, "POST", wipe(sdb), map[string]any{"confirm": "sdb"}, http.StatusBadRequest); c != "validation.confirm" {
		t.Fatalf("bad confirm: %s", c)
	}
	const sd = "mmc-SN32G_0x1234abcd" // the SD card the system runs from
	if c := errCode(t, e, "POST", wipe(sd), map[string]any{"confirm": sd}, http.StatusForbidden); c != "storage.disk_protected" {
		t.Fatalf("system disk: %s", c)
	}

	// sda leaves the mirror and is replaced by sdd: sda becomes obsolete.
	e.createPool("tank", "mirror", demoSDC, demoSDA)
	for _, id := range []string{demoSDC, demoSDA} {
		if c := errCode(t, e, "POST", wipe(id), map[string]any{"confirm": id}, http.StatusConflict); c != "storage.disk_in_pool" && c != "storage.disk_mounted" {
			t.Fatalf("live member %s: %s", id, c)
		}
	}
	e.sys.Fault("unplug", demoSDA)
	e.do("POST", "/api/v1/storage/pools/tank/mount-degraded", map[string]any{"confirm": "tank"}, http.StatusOK, nil)
	e.do("POST", "/api/v1/storage/pools/tank/replace", map[string]any{"old": demoSDA, "new": demoSDD, "confirm": "tank"}, http.StatusAccepted, nil)
	e.advance(2 * time.Minute)
	e.sys.Fault("replug", demoSDA)
	if c := errCode(t, e, "POST", wipe(demoSDD), map[string]any{"confirm": demoSDD}, http.StatusConflict); c != "storage.disk_in_pool" {
		t.Fatalf("new member: %s", c)
	}
	if r := role(demoSDA); r != storage.RoleInUse {
		t.Fatalf("obsolete member role = %s", r)
	}
	e.do("POST", wipe(demoSDA), map[string]any{"confirm": demoSDA}, http.StatusOK, nil)
	if r := role(demoSDA); r != storage.RoleAvailable {
		t.Fatalf("after wipe = %s", r)
	}
}

func TestShareDeleteThenPoolDestroy(t *testing.T) {
	e := newEnv(t)
	e.setup()
	e.createPool("tank", "mirror", demoSDA, demoSDD)
	e.do("POST", "/api/v1/users", map[string]any{"name": "alice", "password": "alice long password"}, http.StatusCreated, nil)
	e.do("POST", "/api/v1/shares", map[string]any{"name": "photos", "pool": "tank",
		"access": []map[string]string{{"user": "alice", "mode": "rw"}}}, http.StatusCreated, nil)
	e.do("POST", "/api/v1/shares/photos/snapshots", nil, http.StatusCreated, nil)

	// The pool holds a share: it cannot be destroyed.
	var refused struct{ Error apiError }
	e.do("DELETE", "/api/v1/storage/pools/tank", map[string]any{"confirm": "tank"}, http.StatusConflict, &refused)
	if refused.Error.Code != "storage.pool_has_shares" {
		t.Fatalf("destroy with shares = %+v", refused.Error)
	}

	// Delete the share: confirmed by name; Samba, data, group and record go.
	if c := errCode(t, e, "DELETE", "/api/v1/shares/photos", map[string]any{"confirm": "foto"}, http.StatusBadRequest); c != "validation.confirm" {
		t.Fatalf("bad confirm: %s", c)
	}
	e.do("DELETE", "/api/v1/shares/photos", map[string]any{"confirm": "photos"}, http.StatusNoContent, nil)
	conf, _ := e.sys.Files.ReadFile("/etc/samba/smb.conf")
	if strings.Contains(string(conf), "[photos]") {
		t.Fatal("share still in smb.conf")
	}
	if e.sys.Files.Exists("/srv/cwnsb/tank/photos") {
		t.Fatal("share subvolume still there")
	}
	var list struct{ Shares []map[string]any }
	e.do("GET", "/api/v1/shares", nil, http.StatusOK, &list)
	if len(list.Shares) != 0 {
		t.Fatalf("shares = %v", list.Shares)
	}
	e.do("DELETE", "/api/v1/shares/photos", map[string]any{"confirm": "photos"}, http.StatusNotFound, nil)

	// Now the empty pool can go; its disks become available again.
	if c := errCode(t, e, "DELETE", "/api/v1/storage/pools/tank", map[string]any{"confirm": "tonk"}, http.StatusBadRequest); c != "validation.confirm" {
		t.Fatalf("bad confirm: %s", c)
	}
	var out struct {
		Result struct {
			Destroyed bool
			Disks     []string
		}
	}
	e.do("DELETE", "/api/v1/storage/pools/tank", map[string]any{"confirm": "tank"}, http.StatusOK, &out)
	if !out.Result.Destroyed || len(out.Result.Disks) != 2 {
		t.Fatalf("destroy = %+v", out)
	}
	var pools struct{ Pools []poolView }
	e.do("GET", "/api/v1/storage/pools", nil, http.StatusOK, &pools)
	if len(pools.Pools) != 0 {
		t.Fatalf("pools = %+v", pools.Pools)
	}
	var disks struct{ Disks []storage.Disk }
	e.do("GET", "/api/v1/storage/disks", nil, http.StatusOK, &disks)
	for _, d := range disks.Disks {
		if (d.ID == demoSDA || d.ID == demoSDD) && d.Role != storage.RoleAvailable {
			t.Fatalf("%s after destroy = %s %+v", d.ID, d.Role, d.Usage)
		}
	}
	// The same name and disks can be used again.
	e.createPool("tank", "mirror", demoSDA, demoSDD)
}

func TestPoolForgetWhenDisksAreGone(t *testing.T) {
	e := newEnv(t)
	e.setup()
	e.createPool("tank", "mirror", demoSDA, demoSDD)
	e.sys.Fault("unplug", demoSDD)
	// One disk still here: the pool must be mounted to be checked empty.
	if c := errCode(t, e, "DELETE", "/api/v1/storage/pools/tank", map[string]any{"confirm": "tank"}, http.StatusConflict); c != "storage.pool_not_mounted" {
		t.Fatalf("destroy with a disk present: %s", c)
	}
	e.sys.Fault("unplug", demoSDA)
	var out struct{ Result struct{ Forgotten bool } }
	e.do("DELETE", "/api/v1/storage/pools/tank", map[string]any{"confirm": "tank"}, http.StatusOK, &out)
	if !out.Result.Forgotten {
		t.Fatalf("forget = %+v", out)
	}
	if e.sys.Files.Exists("/etc/systemd/system/srv-cwnsb-tank.mount") {
		t.Fatal("mount unit left behind")
	}
}

func TestTimeMachineShare(t *testing.T) {
	e := newEnv(t)
	e.setup()
	e.createPool("tank", "mirror", demoSDA, demoSDD)
	e.do("POST", "/api/v1/users", map[string]any{"name": "alice", "password": "alice long password"}, http.StatusCreated, nil)
	body := map[string]any{"name": "macbackup", "pool": "tank", "access": []map[string]string{{"user": "alice", "mode": "rw"}},
		"timeMachine": true, "timeMachineMaxGiB": 800}
	e.do("POST", "/api/v1/shares", body, http.StatusCreated, nil)
	conf, _ := e.sys.Files.ReadFile("/etc/samba/smb.conf")
	if !strings.Contains(string(conf), "fruit:time machine max size = 800G") {
		t.Fatalf("smb.conf:\n%s", conf)
	}
	if c := errCode(t, e, "PUT", "/api/v1/shares/macbackup/timemachine", map[string]any{"enabled": false, "maxSizeGiB": 10}, http.StatusBadRequest); c != "validation.time_machine" {
		t.Fatalf("limit without Time Machine: %s", c)
	}
	var out struct{ Share struct{ TimeMachine bool } }
	e.do("PUT", "/api/v1/shares/macbackup/timemachine", map[string]any{"enabled": false}, http.StatusOK, &out)
	conf, _ = e.sys.Files.ReadFile("/etc/samba/smb.conf")
	if out.Share.TimeMachine || strings.Contains(string(conf), "time machine") {
		t.Fatalf("still Time Machine: %+v\n%s", out, conf)
	}
	e.do("PUT", "/api/v1/shares/nope/timemachine", map[string]any{"enabled": true}, http.StatusNotFound, nil)
}

func TestImportForeignPool(t *testing.T) {
	e := newEnv(t)
	e.setup()
	// Two disks from another CWNSB NAS (or from before a reinstall).
	e.sys.Fault("foreign", demoSDA)
	e.sys.Fault("foreign", demoSDD)
	var scan struct{ Pools []helper.ImportCandidate }
	e.do("GET", "/api/v1/storage/pools/importable", nil, http.StatusOK, &scan)
	if len(scan.Pools) != 1 || scan.Pools[0].Name != "oldnas" || !scan.Pools[0].Complete || scan.Pools[0].Present != 2 || len(scan.Pools[0].Disks) != 2 {
		t.Fatalf("scan = %+v", scan.Pools)
	}
	if c := errCode(t, e, "POST", "/api/v1/storage/pools/import", map[string]any{"name": "oldnas", "confirm": "x"}, http.StatusBadRequest); c != "validation.confirm" {
		t.Fatalf("bad confirm: %s", c)
	}
	if c := errCode(t, e, "POST", "/api/v1/storage/pools/import", map[string]any{"name": "nothere", "confirm": "nothere"}, http.StatusNotFound); c != "storage.pool_not_found" {
		t.Fatalf("unknown pool: %s", c)
	}
	var out struct{ Pool poolView }
	e.do("POST", "/api/v1/storage/pools/import", map[string]any{"name": "oldnas", "confirm": "oldnas"}, http.StatusCreated, &out)
	if out.Pool.Layout != "mirror" || out.Pool.Status == nil || !out.Pool.Status.Mounted || len(out.Pool.Members) != 2 {
		t.Fatalf("imported = %+v status=%+v", out.Pool, out.Pool.Status)
	}
	e.do("GET", "/api/v1/storage/pools/importable", nil, http.StatusOK, &scan)
	if len(scan.Pools) != 0 {
		t.Fatalf("still importable: %+v", scan.Pools)
	}
	if c := errCode(t, e, "POST", "/api/v1/storage/pools/import", map[string]any{"name": "oldnas", "confirm": "oldnas"}, http.StatusConflict); c != "storage.pool_exists" {
		t.Fatalf("second import: %s", c)
	}
}

func TestConfigBackups(t *testing.T) {
	e := newEnv(t)
	e.setup()
	start := time.Now()
	e.sys.Clock = func() time.Time { return start }
	if c := errCode(t, e, "POST", "/api/v1/system/config-backup", nil, http.StatusConflict); c != "backup.no_pool" {
		t.Fatalf("backup without a pool: %s", c)
	}
	e.createPool("tank", "mirror", demoSDA, demoSDD)
	e.createPool("solo", "single", demoSDC)
	var res helper.BackupResult
	e.do("POST", "/api/v1/system/config-backup", nil, http.StatusCreated, &res)
	if len(res.Pools) != 2 {
		t.Fatalf("backup = %+v", res)
	}
	manifest, _ := e.sys.Files.ReadFile("/srv/cwnsb/tank/.cwnsb/backup/" + res.Name + "/manifest.json")
	if !strings.Contains(string(manifest), `"hostname": "raspberrypi"`) {
		t.Fatalf("manifest without the NAS name: %s", manifest)
	}
	db, err := e.sys.Files.ReadFile("/srv/cwnsb/tank/.cwnsb/backup/" + res.Name + "/config.db")
	if err != nil || !strings.HasPrefix(string(db), "SQLite format 3") {
		t.Fatalf("database copy on the pool: %v", err)
	}
	// Seven are kept.
	for i := 0; i < 8; i++ {
		e.advance(time.Hour)
		e.do("POST", "/api/v1/system/config-backup", nil, http.StatusCreated, nil)
	}
	var list helper.BackupList
	e.do("GET", "/api/v1/system/config-backups", nil, http.StatusOK, &list)
	perPool := map[string]int{}
	for _, b := range list.Backups {
		perPool[b.Pool]++
	}
	if perPool["tank"] != 7 || perPool["solo"] != 7 || list.Backups[0].Time < list.Backups[len(list.Backups)-1].Time {
		t.Fatalf("backups = %v (%d listed)", perPool, len(list.Backups))
	}
	// Backups do not keep a pool from being destroyed.
	e.do("DELETE", "/api/v1/storage/pools/solo", map[string]any{"confirm": "solo"}, http.StatusOK, nil)
}

func TestImportFromUSBKey(t *testing.T) {
	e := newEnv(t)
	e.setup()
	start := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC) // copies in the same minute
	e.sys.Clock = func() time.Time { return start }
	e.createPool("tank", "mirror", demoSDA, demoSDD)
	e.do("POST", "/api/v1/shares", map[string]any{"name": "photos", "pool": "tank"}, http.StatusCreated, nil)
	e.sys.Fault("usbkey", demoSDC)

	var src struct{ Sources []helper.ImportSource }
	e.do("GET", "/api/v1/import/sources", nil, http.StatusOK, &src)
	if len(src.Sources) != 1 || src.Sources[0].Label != "USB-KEY" || src.Sources[0].FSType != "exfat" {
		t.Fatalf("sources = %+v", src.Sources)
	}
	uuid := src.Sources[0].UUID
	if c := errCode(t, e, "POST", "/api/v1/import/copy", map[string]any{"uuid": uuid, "share": "photos"}, http.StatusConflict); c != "import.not_mounted" {
		t.Fatalf("copy before mount: %s", c)
	}
	var m helper.ImportMounted
	e.do("POST", "/api/v1/import/mount", map[string]any{"uuid": uuid}, http.StatusOK, &m)
	if len(m.Entries) != 3 {
		t.Fatalf("mounted = %+v", m)
	}
	var started struct{ Dest string }
	e.do("POST", "/api/v1/import/copy", map[string]any{"uuid": uuid, "share": "photos"}, http.StatusAccepted, &started)
	if !strings.HasPrefix(started.Dest, "Import USB-KEY ") {
		t.Fatalf("copy = %+v", started)
	}
	var second struct{ Dest string }
	e.do("POST", "/api/v1/import/copy", map[string]any{"uuid": uuid, "share": "photos"}, http.StatusConflict, nil) // one copy per share at a time
	_ = second
	if c := errCode(t, e, "POST", "/api/v1/import/unmount", map[string]any{"uuid": uuid}, http.StatusConflict); c != "apps.job_running" {
		t.Fatalf("unmount while copying: %s", c)
	}
	e.advance(10 * time.Second)
	var p helper.ImportProgress
	e.do("GET", "/api/v1/import/progress/photos", nil, http.StatusOK, &p)
	if p.Job.State != "running" || p.TotalBytes == 0 || p.CopiedBytes == 0 || p.CopiedBytes >= p.TotalBytes || p.Dest != started.Dest {
		t.Fatalf("half-way = %+v", p)
	}
	e.advance(15 * time.Second)
	e.do("GET", "/api/v1/import/progress/photos", nil, http.StatusOK, &p)
	if p.Job.State != "succeeded" || p.CopiedBytes != p.TotalBytes || p.Files != 4 {
		t.Fatalf("done = %+v", p)
	}
	// A second copy in the same minute goes to its own folder.
	e.do("POST", "/api/v1/import/copy", map[string]any{"uuid": uuid, "share": "photos"}, http.StatusAccepted, &second)
	if second.Dest != started.Dest+" (2)" {
		t.Fatalf("second copy = %q (first %q)", second.Dest, started.Dest)
	}
	e.advance(25 * time.Second)
	e.do("POST", "/api/v1/import/unmount", map[string]any{"uuid": uuid}, http.StatusOK, nil)
	e.do("GET", "/api/v1/import/sources", nil, http.StatusOK, &src)
	if src.Sources[0].Mounted {
		t.Fatal("still mounted")
	}
}

func TestShareRecycleBin(t *testing.T) {
	e := newEnv(t)
	e.setup()
	e.createPool("tank", "mirror", demoSDA, demoSDD)
	e.do("POST", "/api/v1/shares", map[string]any{"name": "docs", "pool": "tank", "recycle": true}, http.StatusCreated, nil)
	var out struct {
		Share struct {
			Recycle     bool
			RecycleDays int
		}
	}
	conf, _ := e.sys.Files.ReadFile("/etc/samba/smb.conf")
	if !strings.Contains(string(conf), "recycle:repository = #recycle") {
		t.Fatal("recycle bin not in smb.conf")
	}
	if c := errCode(t, e, "PUT", "/api/v1/shares/docs/recycle", map[string]any{"enabled": true, "days": 400}, http.StatusBadRequest); c != "validation.recycle" {
		t.Fatalf("400 days: %s", c)
	}
	e.do("PUT", "/api/v1/shares/docs/recycle", map[string]any{"enabled": true, "days": 7}, http.StatusOK, &out)
	if !out.Share.Recycle || out.Share.RecycleDays != 7 {
		t.Fatalf("recycle = %+v", out)
	}
	e.do("PUT", "/api/v1/shares/docs/recycle", map[string]any{"enabled": false}, http.StatusOK, &out)
	conf, _ = e.sys.Files.ReadFile("/etc/samba/smb.conf")
	if out.Share.Recycle || strings.Contains(string(conf), "recycle:") {
		t.Fatalf("still on: %+v", out)
	}
}

func TestLocateDisk(t *testing.T) {
	e := newEnv(t)
	e.setup()
	start := time.Now()
	e.sys.Clock = func() time.Time { return start }
	var out struct{ Seconds int }
	e.do("POST", "/api/v1/storage/disks/"+demoSDA+"/locate", nil, http.StatusAccepted, &out)
	if out.Seconds != 30 {
		t.Fatalf("locate = %+v", out)
	}
	var job struct{ State string }
	e.do("GET", "/api/v1/jobs/locate", nil, http.StatusOK, &job)
	if job.State != "running" {
		t.Fatalf("job = %+v", job)
	}
	e.advance(31 * time.Second)
	e.do("GET", "/api/v1/jobs/locate", nil, http.StatusOK, &job)
	if job.State != "succeeded" {
		t.Fatalf("job after 30 s = %+v", job)
	}
	e.do("POST", "/api/v1/storage/disks/wwn-0xdead/locate", nil, http.StatusNotFound, nil)
}

// A disk whose partition table alone was removed elsewhere looks blank but
// hides an old NTFS where the pool's partition starts: refused, then accepted
// once NasBuster's own erase zeroed the start of the disk.
func TestPoolCreateRefusesResidualUntilErased(t *testing.T) {
	e := newEnv(t)
	e.setup()
	if err := e.sys.Fault("residual", demoSDA); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Error apiError `json:"error"`
	}
	body := map[string]any{"name": "tank", "layout": "single", "disks": []string{demoSDA}, "confirm": "tank"}
	e.do("POST", "/api/v1/storage/pools", body, http.StatusConflict, &out)
	if out.Error.Code != "storage.disk_residual" || out.Error.Params["disk"] != demoSDA || out.Error.Params["found"] != "ntfs" {
		t.Fatalf("residual: %+v", out.Error)
	}
	e.do("POST", "/api/v1/storage/disks/"+demoSDA+"/wipe", map[string]any{"confirm": demoSDA}, http.StatusOK, nil)
	e.createPool("tank", "single", demoSDA)
}
