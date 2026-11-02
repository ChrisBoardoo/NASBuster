// SPDX-License-Identifier: AGPL-3.0-or-later

package scheduler

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crosswax/nasbuster/internal/alerts"
	"github.com/crosswax/nasbuster/internal/demo"
	"github.com/crosswax/nasbuster/internal/events"
	fx "github.com/crosswax/nasbuster/internal/fixture"
	"github.com/crosswax/nasbuster/internal/helper"
	"github.com/crosswax/nasbuster/internal/resync"
	"github.com/crosswax/nasbuster/internal/storage"
	"github.com/crosswax/nasbuster/internal/store"
	"github.com/crosswax/nasbuster/internal/sysexec"
	"github.com/crosswax/nasbuster/internal/system"
)

type fixture struct {
	demo  *demo.System
	st    *store.Store
	sched *Scheduler
	hc    helper.Client
	clock time.Time
	boot  string // boot id the scheduler sees
}

// newFixture: emulated Pi with a mirror pool "tank" and a share "photos".
func newFixture(t *testing.T) *fixture {
	t.Helper()
	return newPoolFixture(t, "mirror", "wwn-0x5000c500c1234567", "wwn-0x50014ee2b1234567")
}

// newPoolFixture: an emulated Pi with the pool "tank" (layout on disks) and its share "photos".
func newPoolFixture(t *testing.T, layout string, disks ...string) *fixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	f := &fixture{st: st, clock: time.Date(2026, 10, 1, 21, 30, 0, 0, time.UTC)}
	sys := demo.New()
	sys.Clock = func() time.Time { return f.clock }
	f.demo = sys
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	discoverer := &storage.Discoverer{Run: sys.Runner, FS: sys.FS}
	f.hc = &helper.LocalClient{Ops: &helper.Ops{
		Run: sys.Runner, FS: sys.FS, Logger: logger, Discover: discoverer.Disks, Files: sys.Files,
		ByIDDir: "/dev/disk/by-id", DeviceExists: sys.DeviceExists, WriteSysfs: sys.WriteSysfs,
		UnitDir: "/etc/systemd/system", MDConf: "/etc/mdadm/mdadm.conf", SMBConf: "/etc/samba/smb.conf",
		Sleep: func(time.Duration) {}, Now: sys.Clock,
	}}
	var pool helper.PoolCreateResult
	if err := f.hc.Call(ctx, helper.OpPoolCreate, helper.PoolCreateArgs{Name: "tank", Layout: layout,
		Disks: disks, Confirm: "tank"}, &pool); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreatePool(ctx, store.Pool{Name: "tank", Layout: layout, UUID: pool.UUID, Members: pool.Members}); err != nil {
		t.Fatal(err)
	}
	if err := f.hc.Call(ctx, helper.OpShareCreate, helper.ShareCreateArgs{Pool: "tank", Name: "photos"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateShare(ctx, store.Share{Name: "photos", Pool: "tank"}); err != nil {
		t.Fatal(err)
	}
	f.boot = "b1"
	f.sched = &Scheduler{Store: st, Helper: f.hc, Logger: logger, Now: func() time.Time { return f.clock },
		Boot: func() (string, time.Duration, bool) { return f.boot, time.Hour, true }}
	return f
}

func (f *fixture) snapshots(t *testing.T) []string {
	t.Helper()
	var res helper.SnapshotListResult
	if err := f.hc.Call(context.Background(), helper.OpSnapshotList, helper.SnapshotArgs{Pool: "tank", Share: "photos"}, &res); err != nil {
		t.Fatal(err)
	}
	return res.Snapshots
}

func TestHourlySnapshotsAndRetention(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if r := f.sched.Tick(ctx); r.Snapshots != 1 || len(r.Errors) != 0 {
		t.Fatalf("first tick = %+v", r)
	}
	f.clock = f.clock.Add(20 * time.Minute) // same clock hour
	if r := f.sched.Tick(ctx); r.Snapshots != 0 {
		t.Fatalf("second snapshot in the same hour: %+v", r)
	}
	pruned := 0
	for h := 0; h < 30; h++ { // 30 hours, ticking every hour
		f.clock = f.clock.Add(time.Hour)
		pruned += f.sched.Tick(ctx).Pruned
	}
	if got := len(f.snapshots(t)); got > 24+7 || got < 24 {
		t.Fatalf("%d snapshots after 30 h (want 24..31, the policy prunes the rest)", got)
	}
	if pruned == 0 {
		t.Fatal("nothing was pruned")
	}

	// Downtime: 5 hours off, one catch-up snapshot at restart, not five.
	f.clock = f.clock.Add(5 * time.Hour)
	if r := f.sched.Tick(ctx); r.Snapshots != 1 {
		t.Fatalf("catch-up = %+v", r)
	}

	var audit []store.AuditEntry
	audit, _ = f.st.AuditList(ctx, 5)
	if audit[0].Actor != "scheduler" || audit[0].Action != "snapshot.auto" {
		t.Fatalf("audit = %+v", audit[0])
	}
}

func TestDisabledSnapshots(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := DefaultProtection
	p.Snapshots.Enabled = false
	f.st.SetSetting(ctx, ProtectionKey, p)
	if r := f.sched.Tick(ctx); r.Snapshots != 0 {
		t.Fatalf("snapshots while disabled: %+v", r)
	}
}

func TestMonthlyScrub(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.clock = time.Now() // the pool was created now (store clock)
	if r := f.sched.Tick(ctx); r.Scrubs != 0 {
		t.Fatalf("scrub right after creation: %+v", r)
	}
	f.clock = f.clock.Add(31 * 24 * time.Hour)
	if r := f.sched.Tick(ctx); r.Scrubs != 1 {
		t.Fatalf("no scrub after 31 days: %+v", r)
	}
	f.clock = f.clock.Add(time.Hour)
	if r := f.sched.Tick(ctx); r.Scrubs != 0 {
		t.Fatalf("scrubbed again an hour later: %+v", r)
	}
	st, _ := LoadState(ctx, f.st)
	if st.LastScrub["tank"] == 0 {
		t.Fatal("last scrub not recorded")
	}
}

func TestProtectionValid(t *testing.T) {
	p := DefaultProtection
	if !p.Valid() {
		t.Fatal("defaults invalid")
	}
	p.Scrub.IntervalDays = 1
	if p.Valid() {
		t.Fatal("daily scrub accepted (too heavy for a Pi)")
	}
}

func TestAlertsNotifyOnceAndResolve(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	var mu sync.Mutex
	var titles []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		titles = append(titles, string(b))
		mu.Unlock()
	}))
	defer srv.Close()
	f.st.SetSetting(ctx, NotificationsKey, alerts.Channels{Ntfy: alerts.NtfyConfig{Enabled: true, URL: srv.URL + "/nas"}})

	// The emulated Pi's firmware reports an under-voltage since boot (0x50000).
	sys := demo.New()
	f.sched.Prober = system.New(sys.FS, sys.Runner)
	f.sched.Notifier = alerts.NewNotifier()
	f.sched.Events = &events.Recorder{Store: f.st, Now: func() time.Time { return f.clock }}
	if r := f.sched.Tick(ctx); r.AlertsRaised != 1 {
		t.Fatalf("first evaluation = %+v", r)
	}
	f.clock = f.clock.Add(10 * time.Minute)
	if r := f.sched.Tick(ctx); r.AlertsRaised != 0 || r.AlertsResolved != 0 {
		t.Fatalf("still holding must not notify again: %+v", r)
	}
	// Power fixed (new boot, clean flags): resolved and notified.
	f.sched.Prober = system.New(fx.Parse([]byte("-- sys/devices/platform/soc/soc:firmware/get_throttled --\n0\n")), sysexec.NewFake())
	f.clock = f.clock.Add(10 * time.Minute)
	if r := f.sched.Tick(ctx); r.AlertsResolved != 1 {
		t.Fatalf("resolution = %+v", r)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(titles) != 2 || !strings.Contains(titles[0], "Under-voltage happened") || !strings.HasPrefix(titles[1], "Resolved: ") {
		t.Fatalf("notifications = %q", titles)
	}
	list, _ := f.st.Alerts(ctx, 0)
	if len(list) != 1 || list[0].ResolvedAt == 0 {
		t.Fatalf("alerts = %+v", list)
	}
	// The journal keeps both moments (ADR-0031), not the 10 minutes in between.
	journal, _ := f.st.Events(ctx, store.EventFilter{})
	if len(journal) != 2 || journal[1].Code != "power.under_voltage_occurred" || journal[1].Phase != "raised" ||
		journal[0].Phase != "resolved" || journal[0].Time != f.clock.Unix() || journal[0].Subject != "system" {
		t.Fatalf("journal = %+v", journal)
	}
}

// A member replaced by a bigger disk keeps the old size until a resize (seen
// on the Pi 5): the scheduler grows it, through the restore that resizes first.
func TestGrowAfterABiggerDisk(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	var st storage.PoolStatus
	if err := f.hc.Call(ctx, helper.OpPoolStatus, helper.PoolArgs{Name: "tank"}, &st); err != nil {
		t.Fatal(err)
	}
	grown := func() bool {
		list, _ := f.st.AuditList(ctx, 10)
		return slices.ContainsFunc(list, func(a store.AuditEntry) bool { return a.Action == "pool.grow" })
	}
	var r Report
	st.Slack = 7168 // alignment only
	f.sched.restoreRedundancy(ctx, "tank", st, &r)
	if grown() || len(r.Errors) != 0 {
		t.Fatalf("grown for 7 KiB of slack: %+v", r)
	}
	st.Slack = 1776294912
	f.sched.restoreRedundancy(ctx, "tank", st, &r)
	if !grown() || r.Restores != 0 || len(r.Errors) != 0 {
		t.Fatalf("not grown: %+v", r)
	}
}

func TestRestoreRedundancyAutomatically(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	sys := f.demo
	// A replaced disk left single chunks behind (writes while degraded).
	if err := sys.Fault("unplug", "wwn-0x50014ee2b1234567"); err != nil {
		t.Fatal(err)
	}
	if err := f.hc.Call(ctx, helper.OpPoolMountDegraded, helper.PoolConfirmArgs{Name: "tank", Confirm: "tank"}, nil); err != nil {
		t.Fatal(err)
	}
	resync.Mark(ctx, f.st, "tank", f.clock) // as the API does after a degraded mount
	// While a disk is missing, the scheduler does not try.
	if r := f.sched.Tick(ctx); r.Restores != 0 {
		t.Fatalf("restore with a disk missing: %+v", r)
	}
	if err := f.hc.Call(ctx, helper.OpPoolReplace, helper.PoolReplaceArgs{Name: "tank", Old: "wwn-0x50014ee2b1234567",
		New: "wwn-0x5000c500c1234567", Confirm: "tank"}, nil); err == nil {
		t.Fatal("replace with a pool member must fail")
	}
	if err := f.hc.Call(ctx, helper.OpPoolReplace, helper.PoolReplaceArgs{Name: "tank", Old: "wwn-0x50014ee2b1234567",
		New: "usb-Seagate_ST2000LM015-2E8174_0123456789ABCDEF-0:0", Confirm: "tank"}, nil); err == nil {
		t.Fatal("a 2 TB disk cannot replace a 4 TB one")
	}
	// The disk is back, but a pool mounted degraded does not reopen it (ADR-0027):
	// nothing is restored while it is unused.
	sys.Fault("replug", "wwn-0x50014ee2b1234567")
	f.clock = f.clock.Add(10 * time.Minute)
	if r := f.sched.Tick(ctx); r.Restores != 0 {
		t.Fatalf("restore with the disk unused: %+v", r)
	}
	var st storage.PoolStatus
	if err := f.hc.Call(ctx, helper.OpPoolStatus, helper.PoolArgs{Name: "tank"}, &st); err != nil || !st.CanResume {
		t.Fatalf("disk back = %+v %v", st, err)
	}
	// The NAS restarts: a normal mount opens both disks, the single chunks remain.
	unit := storage.MountUnitName("tank")
	sys.Runner.Run(ctx, "systemctl", "stop", unit)
	sys.Runner.Run(ctx, "systemctl", "start", unit)
	f.clock = f.clock.Add(10 * time.Minute)
	if r := f.sched.Tick(ctx); r.Restores != 1 {
		t.Fatalf("no automatic restore: %+v", r)
	}
	f.clock = f.clock.Add(10 * time.Minute)
	second := f.sched.Tick(ctx)
	if second.Restores != 0 {
		t.Fatalf("restore started twice: %+v", second)
	}
	audit, _ := f.st.AuditList(ctx, 20)
	found := false
	for _, a := range audit {
		found = found || (a.Action == "pool.restore_redundancy" && a.Actor == "scheduler" && a.Detail == "automatic")
	}
	if !found {
		t.Fatalf("restore not audited: %+v", audit)
	}

	// Then both copies are checked once (ADR-0027): a scrub was owed since the
	// pool ran with a disk missing, and only a later scrub clears that. It
	// starts as soon as the conversion is over (here, at the second tick).
	scrubs := second.Scrubs
	for i := 0; i < 6; i++ {
		f.clock = f.clock.Add(10 * time.Minute)
		scrubs += f.sched.Tick(ctx).Scrubs
	}
	if scrubs != 1 {
		t.Fatalf("resync scrubs = %d, want 1", scrubs)
	}
	audit, _ = f.st.AuditList(ctx, 50)
	found = false
	for _, a := range audit {
		found = found || (a.Action == "pool.scrub" && a.Actor == "scheduler" && a.Detail == "resync")
	}
	var marks map[string]int64
	f.st.GetSetting(ctx, "pools.resync", &marks)
	if !found || marks["tank"] != 0 {
		t.Fatalf("resync scrub audited: %v, marks = %v", found, marks)
	}
}

func TestTimeMachineSharesAreNotSnapshotted(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.hc.Call(ctx, helper.OpShareCreate, helper.ShareCreateArgs{Pool: "tank", Name: "macbackup"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.CreateShare(ctx, store.Share{Name: "macbackup", Pool: "tank", TimeMachine: true}); err != nil {
		t.Fatal(err)
	}
	if r := f.sched.Tick(ctx); r.Snapshots != 1 { // photos only
		t.Fatalf("tick = %+v", r)
	}
	var res helper.SnapshotListResult
	f.hc.Call(ctx, helper.OpSnapshotList, helper.SnapshotArgs{Pool: "tank", Share: "macbackup"}, &res)
	if len(res.Snapshots) != 0 {
		t.Fatalf("Time Machine share snapshotted: %v", res.Snapshots)
	}
}

const hubSda, hubSdc, hubSdd = "wwn-0x5000c500c1234567", "usb-Seagate_ST2000LM015-2E8174_0123456789ABCDEF-0:0", "wwn-0x50014ee2b1234567"

// hubCut: a parity pool loses two disks at once, the scheduler sees it, they
// come back and the pool is reassembled. It returns the status reader.
func hubCut(t *testing.T) (*fixture, func() storage.PoolStatus) {
	t.Helper()
	f := newPoolFixture(t, "parity1", hubSda, hubSdc, hubSdd)
	ctx := context.Background()
	status := func() storage.PoolStatus {
		t.Helper()
		var st storage.PoolStatus
		if err := f.hc.Call(ctx, helper.OpPoolStatus, helper.PoolArgs{Name: "tank"}, &st); err != nil {
			t.Fatal(err)
		}
		if err := resync.Apply(ctx, f.st, &st, f.clock, f.boot); err != nil {
			t.Fatal(err)
		}
		return st
	}
	fault := func(kind, disk string) {
		t.Helper()
		if err := f.demo.Fault(kind, disk); err != nil {
			t.Fatal(err)
		}
	}
	fault("unplug", hubSdc)
	fault("unplug", hubSdd)
	// Seen broken (the scheduler, or the page before the click): the repair
	// and the scrub are owed before anything is forced.
	if st := status(); !st.RepairNeeded || !st.ResyncNeeded {
		t.Fatalf("broken pool owes nothing: %+v", st)
	}
	fault("replug", hubSdc)
	fault("replug", hubSdd)
	if err := f.hc.Call(ctx, helper.OpPoolReassemble, helper.PoolReassembleArgs{Name: "tank", Confirm: "tank"}, nil); err != nil {
		t.Fatal(err)
	}
	return f, status
}

// After a forced assembly (ADR-0032), md repairs the parity once every member
// is in sync, then the scrub checks the files: never both at once. A repair
// that a reboot cut is started again.
func TestParityRepairBeforeTheScrub(t *testing.T) {
	f, status := hubCut(t)
	ctx := context.Background()

	// md still rebuilding a member: nothing yet, and no scrub either.
	var r Report
	rebuilding := status()
	rebuilding.MD.Sync = &storage.MDSync{Action: "recovery", Percent: 12}
	if !f.sched.repairParity(ctx, "tank", rebuilding, &r) || r.Repairs != 0 || len(r.Errors) != 0 {
		t.Fatalf("repair during a rebuild: %+v", r)
	}

	// Every member in sync: the repair starts, the scrub waits. Still owed
	// until it is seen at its end.
	repairAt := f.clock
	if r = f.sched.Tick(ctx); r.Repairs != 1 || r.Scrubs != 0 || len(r.Errors) != 0 {
		t.Fatalf("first pass = %+v", r)
	}
	st := status()
	if !st.RepairNeeded || !st.ResyncNeeded || st.MD.Sync == nil || st.MD.Sync.Action != "repair" || st.Health != storage.HealthDegraded {
		t.Fatalf("repairing = %+v md=%+v", st, st.MD)
	}
	// Still repairing at the next pass: no scrub on top of it.
	f.clock = f.clock.Add(5*time.Minute + time.Second)
	f.demo.Clock = func() time.Time { return repairAt.Add(40 * time.Second) } // into the 45 s repair
	if r = f.sched.Tick(ctx); r.Scrubs != 0 || r.Repairs != 0 {
		t.Fatalf("scrub during the repair: %+v", r)
	}
	// A reboot: md is idle and does not resume the repair. Started again.
	f.boot = "b2"
	f.demo.Clock = func() time.Time { return f.clock }
	f.clock = f.clock.Add(5*time.Minute + time.Second)
	if r = f.sched.Tick(ctx); r.Repairs != 1 || r.Scrubs != 0 || len(r.Errors) != 0 {
		t.Fatalf("after the reboot = %+v", r)
	}
	// This time it reaches its end: the scrub; once it ends, the pool is healthy.
	f.clock = f.clock.Add(5*time.Minute + time.Second)
	if r = f.sched.Tick(ctx); r.Scrubs != 1 || r.Repairs != 0 || len(r.Errors) != 0 {
		t.Fatalf("after the repair = %+v", r)
	}
	f.clock = f.clock.Add(2 * time.Minute)
	if st = status(); st.ResyncNeeded || st.RepairNeeded || st.Health != storage.HealthOK {
		t.Fatalf("after the scrub = %+v", st)
	}
}

// A member that drops during the repair stops it (md keeps where in
// sync_min): taken back, the repair starts again over the whole array.
func TestParityRepairAfterAMemberDrops(t *testing.T) {
	f, status := hubCut(t)
	ctx := context.Background()
	if r := f.sched.Tick(ctx); r.Repairs != 1 {
		t.Fatalf("first pass = %+v", r)
	}
	if err := f.demo.Fault("unplug", hubSdc); err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(5*time.Minute + time.Second)
	if r := f.sched.Tick(ctx); r.Repairs != 0 || r.Scrubs != 0 {
		t.Fatalf("degraded = %+v", r)
	}
	if err := f.demo.Fault("replug", hubSdc); err != nil {
		t.Fatal(err)
	}
	if err := f.hc.Call(ctx, helper.OpPoolResume, helper.PoolResumeArgs{Name: "tank", Confirm: "tank"}, nil); err != nil {
		t.Fatal(err)
	}
	if st := status(); !st.RepairNeeded || st.MD == nil || !st.MD.Interrupted || st.MD.Degraded() {
		t.Fatalf("taken back = %+v md=%+v", st, st.MD)
	}
	f.clock = f.clock.Add(5*time.Minute + time.Second)
	if r := f.sched.Tick(ctx); r.Repairs != 1 || r.Scrubs != 0 || len(r.Errors) != 0 {
		t.Fatalf("taken back, next pass = %+v", r)
	}
	// Over the whole array: sync_min was set back to 0 before it started.
	if st := status(); st.MD.Sync == nil || st.MD.Sync.Action != "repair" {
		t.Fatalf("restarted = %+v", st.MD)
	}
	f.clock = f.clock.Add(5*time.Minute + time.Second)
	if r := f.sched.Tick(ctx); r.Scrubs != 1 || len(r.Errors) != 0 {
		t.Fatalf("after the repair = %+v", r)
	}
	if st := status(); st.RepairNeeded || st.MD.Interrupted {
		t.Fatalf("repaired = %+v md=%+v", st, st.MD)
	}
}

// A parity pool writes mdadm.conf: the helper rebuilds the initramfs at once,
// the unit records it, and the hourly check finds nothing more to do (ADR-0033).
func TestInitramfsRebuiltAfterAParityPool(t *testing.T) {
	f := newPoolFixture(t, "parity1", hubSda, hubSdc, hubSdd)
	ctx := context.Background()
	if !f.demo.Files.Exists("/var/lib/cwnsb-helper/initramfs-mdadm.conf.pending") {
		t.Fatal("creating the pool did not start the rebuild")
	}
	f.clock = f.clock.Add(time.Minute) // the rebuild takes 30 s in the demo
	if r := f.sched.Tick(ctx); len(r.Errors) != 0 {
		t.Fatalf("tick = %+v", r)
	}
	stamp, _ := f.demo.Files.ReadFile("/var/lib/cwnsb-helper/initramfs-mdadm.conf")
	conf, _ := f.demo.Files.ReadFile("/etc/mdadm/mdadm.conf")
	if len(conf) == 0 || string(stamp) != string(conf) {
		t.Fatalf("stamp = %q, mdadm.conf = %q", stamp, conf)
	}
	var res helper.InitramfsResult
	if err := f.hc.Call(ctx, helper.OpInitramfsRefresh, struct{}{}, &res); err != nil || res.State != "current" {
		t.Fatalf("refresh = %+v, %v", res, err)
	}
}

// mdadm.conf changes while a rebuild runs (a second pool, seconds after the
// first): the scheduler follows the rebuild minute by minute and starts the
// next one, instead of waiting an hour (review of 2026-10-27).
func TestInitramfsChangeDuringARebuildCaughtWithinMinutes(t *testing.T) {
	f := newPoolFixture(t, "parity1", hubSda, hubSdc, hubSdd)
	ctx := context.Background()
	f.sched.Tick(ctx) // the pools changed: checked at once, the rebuild runs
	conf, _ := f.demo.Files.ReadFile("/etc/mdadm/mdadm.conf")
	second := string(conf) + "# BEGIN CrossWax NasBuster pool e3\nARRAY /dev/md/cwnsb-e3 metadata=1.2 UUID=11111111:22222222:33333333:44444444\n# END CrossWax NasBuster pool e3\n"
	f.demo.Files.WriteFile("/etc/mdadm/mdadm.conf", []byte(second), 0o644)

	for range 3 { // the first rebuild ends (30 s), the next one starts and ends
		f.clock = f.clock.Add(time.Minute)
		if r := f.sched.Tick(ctx); len(r.Errors) != 0 {
			t.Fatalf("tick = %+v", r)
		}
	}
	if stamp, _ := f.demo.Files.ReadFile("/var/lib/cwnsb-helper/initramfs-mdadm.conf"); string(stamp) != second {
		t.Fatalf("after 3 minutes, the initramfs has %q", stamp)
	}
}
