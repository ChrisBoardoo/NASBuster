// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/crosswax/nasbuster/internal/alerts"
	"github.com/crosswax/nasbuster/internal/auth"
	"github.com/crosswax/nasbuster/internal/demo"
	"github.com/crosswax/nasbuster/internal/helper"
	"github.com/crosswax/nasbuster/internal/metrics"
	"github.com/crosswax/nasbuster/internal/storage"
	"github.com/crosswax/nasbuster/internal/store"
	"github.com/crosswax/nasbuster/internal/system"
)

type fakeSetupCode struct {
	mu   sync.Mutex
	code string
}

func (f *fakeSetupCode) Current() string { f.mu.Lock(); defer f.mu.Unlock(); return f.code }
func (f *fakeSetupCode) Consume()        { f.mu.Lock(); defer f.mu.Unlock(); f.code = "" }

const testCode = "K7QF-92MD"

type env struct {
	t      *testing.T
	sys    *demo.System
	h      http.Handler
	srv    *httptest.Server
	client *http.Client
	csrf   string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	return newEnvWith(t, nil)
}

// newEnvWith lets a test adjust the dependencies (nil: none).
func newEnvWith(t *testing.T, adjust func(*Deps)) *env {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	sys := demo.New()
	prober := system.New(sys.FS, sys.Runner)
	prober.GOARCH = "arm64"
	sampler := metrics.New(prober)
	sampler.Tick(time.Now())
	helperDisks := &storage.Discoverer{Run: sys.Runner, FS: sys.FS}
	exportPath := filepath.Join(t.TempDir(), "config.db")
	ops := &helper.Ops{
		Run: sys.Runner, FS: sys.FS, ByIDDir: "/dev/disk/by-id",
		DeviceExists: sys.DeviceExists, WriteFile: sys.WriteFile, WriteSysfs: sys.WriteSysfs,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Discover: helperDisks.Disks, Files: sys.Files,
		UnitDir: "/etc/systemd/system", MDConf: "/etc/mdadm/mdadm.conf", SMBConf: "/etc/samba/smb.conf", Now: func() time.Time { return sys.Clock() },
		Sleep:            func(time.Duration) {},
		ReadDaemonExport: func() ([]byte, error) { return os.ReadFile(exportPath) },
	}
	ui := fstest.MapFS{
		"index.html":    {Data: []byte("<!doctype html><title>CWNSB</title>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}
	deps := Deps{
		Store:      st,
		Prober:     prober,
		Disks:      &storage.Discoverer{Run: sys.Runner, FS: sys.FS},
		Helper:     &helper.LocalClient{Ops: ops},
		Metrics:    sampler,
		Limiter:    auth.NewLimiter(),
		SetupCode:  &fakeSetupCode{code: testCode},
		UI:         fs.FS(ui),
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Version:    "test",
		Demo:       true,
		ExportPath: exportPath,
		Now:        func() time.Time { return sys.Clock() },
		AdminCheck: func(context.Context, string, int) (bool, error) { return false, nil },
	}
	if adjust != nil {
		adjust(&deps)
	}
	h := New(deps)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	// Generous: argon2 logins on a loaded machine (a game, the lab running)
	// once made a static file wait more than 10 s.
	e := &env{t: t, sys: sys, h: h, srv: srv, client: &http.Client{Jar: jar, Timeout: 60 * time.Second}}
	e.bootstrap()
	return e
}

func (e *env) bootstrap() map[string]any {
	var out map[string]any
	e.do("GET", "/api/v1/bootstrap", nil, http.StatusOK, &out)
	u, _ := url.Parse(e.srv.URL)
	for _, c := range e.client.Jar.Cookies(u) {
		if c.Name == csrfCookie {
			e.csrf = c.Value
		}
	}
	return out
}

// do sends a request with the CSRF header and checks the status.
func (e *env) do(method, path string, body any, wantStatus int, out any) *http.Response {
	e.t.Helper()
	return e.raw(method, path, body, map[string]string{"X-CSRF-Token": e.csrf}, wantStatus, out)
}

func (e *env) raw(method, path string, body any, headers map[string]string, wantStatus int, out any) *http.Response {
	e.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		e.t.Fatalf("%s %s = %d, want %d: %s", method, path, resp.StatusCode, wantStatus, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			e.t.Fatalf("%s %s: decode %q: %v", method, path, data, err)
		}
	}
	return resp
}

func errCode(t *testing.T, e *env, method, path string, body any, status int) string {
	t.Helper()
	var out struct {
		Error apiError `json:"error"`
	}
	e.do(method, path, body, status, &out)
	return out.Error.Code
}

func (e *env) setup() {
	e.t.Helper()
	e.do("POST", "/api/v1/setup/admin", map[string]string{
		"setupCode": "k7qf 92md", "username": "admin", "password": "correct horse battery", "language": "fr",
	}, http.StatusCreated, nil)
}

func TestSetupFlow(t *testing.T) {
	e := newEnv(t)
	boot := e.bootstrap()
	if boot["needsSetup"] != true || boot["authenticated"] != false || e.csrf == "" {
		t.Fatalf("bootstrap = %v, csrf=%q", boot, e.csrf)
	}

	// Health checks are public before setup.
	var checks struct{ Checks []system.Check }
	e.do("GET", "/api/v1/setup/checks", nil, http.StatusOK, &checks)
	if len(checks.Checks) != 7 {
		t.Fatalf("checks = %+v", checks)
	}

	if c := errCode(t, e, "POST", "/api/v1/setup/admin", map[string]string{"setupCode": "AAAA-BBBB", "username": "admin", "password": "correct horse battery"}, http.StatusForbidden); c != "setup.invalid_code" {
		t.Fatalf("wrong code: %s", c)
	}
	if c := errCode(t, e, "POST", "/api/v1/setup/admin", map[string]string{"setupCode": testCode, "username": "admin", "password": "short"}, http.StatusBadRequest); c != "validation.password_too_short" {
		t.Fatalf("short password: %s", c)
	}
	e.setup()

	boot = e.bootstrap()
	if boot["needsSetup"] != false || boot["authenticated"] != true {
		t.Fatalf("after setup bootstrap = %v", boot)
	}
	if c := errCode(t, e, "GET", "/api/v1/setup/checks", nil, http.StatusForbidden); c != "setup.already_done" {
		t.Fatalf("checks after setup: %s", c)
	}
	if c := errCode(t, e, "POST", "/api/v1/setup/admin", map[string]string{"setupCode": testCode, "username": "evil", "password": "correct horse battery"}, http.StatusConflict); c != "setup.already_done" {
		t.Fatalf("second setup: %s", c)
	}
	var me struct{ User store.User }
	e.do("GET", "/api/v1/auth/me", nil, http.StatusOK, &me)
	if me.User.Username != "admin" || me.User.Language != "fr" || me.User.PasswordHash != "" {
		t.Fatalf("me = %+v", me.User)
	}
}

func TestCSRFAndOrigin(t *testing.T) {
	e := newEnv(t)
	body := map[string]string{"username": "a", "password": "b"}
	var out struct{ Error apiError }
	e.raw("POST", "/api/v1/auth/login", body, nil, http.StatusForbidden, &out)
	if out.Error.Code != "csrf.invalid" {
		t.Fatalf("no token: %+v", out)
	}
	e.raw("POST", "/api/v1/auth/login", body, map[string]string{"X-CSRF-Token": "forged"}, http.StatusForbidden, nil)
	e.raw("POST", "/api/v1/auth/login", body, map[string]string{"X-CSRF-Token": e.csrf, "Origin": "https://evil.example"}, http.StatusForbidden, nil)
	e.raw("POST", "/api/v1/auth/login", body, map[string]string{"X-CSRF-Token": e.csrf, "Origin": e.srv.URL}, http.StatusUnauthorized, nil)

	req, _ := http.NewRequest("POST", e.srv.URL+"/api/v1/auth/login", strings.NewReader("username=a"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", e.csrf)
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("form body = %d, want 415", resp.StatusCode)
	}
}

func TestLoginAndRateLimit(t *testing.T) {
	e := newEnv(t)
	e.setup()
	e.do("POST", "/api/v1/auth/logout", nil, http.StatusNoContent, nil)
	if c := errCode(t, e, "GET", "/api/v1/auth/me", nil, http.StatusUnauthorized); c != "auth.required" {
		t.Fatalf("after logout: %s", c)
	}
	e.do("POST", "/api/v1/auth/login", map[string]string{"username": "Admin", "password": "correct horse battery"}, http.StatusOK, nil)
	e.do("GET", "/api/v1/auth/me", nil, http.StatusOK, nil)

	for i := 0; i < 5; i++ {
		e.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "wrong"}, http.StatusUnauthorized, nil)
	}
	e.do("POST", "/api/v1/auth/login", map[string]string{"username": "nobody", "password": "wrong"}, http.StatusUnauthorized, nil)
	var out struct{ Error apiError }
	resp := e.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "correct horse battery"}, http.StatusTooManyRequests, &out)
	if out.Error.Code != "auth.rate_limited" || out.Error.Params["retryAfter"] == nil || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("rate limit = %+v", out)
	}
}

func TestSetupCodeGlobalLimit(t *testing.T) {
	e := newEnv(t)
	// Each attempt comes from a different LAN address: per-IP limits alone
	// would never trigger. The global limit must.
	attempt := func(i int, code string) int {
		b, _ := json.Marshal(map[string]string{"setupCode": code, "username": "admin", "password": "correct horse battery"})
		req := httptest.NewRequest("POST", "/api/v1/setup/admin", bytes.NewReader(b))
		req.RemoteAddr = "192.168.1." + strconv.Itoa(10+i) + ":40000"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", "csrf-token-for-test-0123456789abcdef")
		req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "csrf-token-for-test-0123456789abcdef"})
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec.Code
	}
	for i := 0; i < 6; i++ { // 5 free failures, the 6th locks
		if code := attempt(i, "AAAA-BBBB"); code != http.StatusForbidden {
			t.Fatalf("attempt %d = %d, want 403", i, code)
		}
	}
	// A fresh address with the right code is refused while locked.
	if code := attempt(99, testCode); code != http.StatusTooManyRequests {
		t.Fatalf("after global lock = %d, want 429", code)
	}
}

func TestFailedLoginDoesNotLogSecrets(t *testing.T) {
	e := newEnv(t)
	e.setup()
	e.do("POST", "/api/v1/auth/login", map[string]string{"username": "My Secret Passw0rd!", "password": "x"}, http.StatusUnauthorized, nil)
	var audit struct{ Entries []store.AuditEntry }
	e.do("GET", "/api/v1/audit", nil, http.StatusOK, &audit)
	for _, en := range audit.Entries {
		if strings.Contains(strings.ToLower(en.Actor), "secret") {
			t.Fatalf("password-like username logged: %+v", en)
		}
	}
}

func TestAuthRequired(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/api/v1/system/info", "/api/v1/storage/disks", "/api/v1/events", "/api/v1/audit", "/api/v1/onboarding"} {
		if c := errCode(t, e, "GET", p, nil, http.StatusUnauthorized); c != "auth.required" {
			t.Errorf("%s: %s", p, c)
		}
	}
}

func TestSystemAndStorage(t *testing.T) {
	e := newEnv(t)
	e.setup()

	var info system.Info
	e.do("GET", "/api/v1/system/info", nil, http.StatusOK, &info)
	if !info.IsRaspberryPi || info.BootDevice.Kind != "sd_card" || info.CPUCores != 4 {
		t.Fatalf("info = %+v", info)
	}

	var disks struct{ Disks []storage.Disk }
	e.do("GET", "/api/v1/storage/disks", nil, http.StatusOK, &disks)
	if len(disks.Disks) != 5 {
		t.Fatalf("disks = %d", len(disks.Disks))
	}

	var smart storage.SmartReport
	e.do("GET", "/api/v1/storage/disks/wwn-0x5000c500c1234567/smart", nil, http.StatusOK, &smart)
	if !smart.Available || smart.Passed == nil || !*smart.Passed {
		t.Fatalf("smart = %+v", smart)
	}
	e.do("GET", "/api/v1/storage/disks/"+url.PathEscape("usb-WDC_WD40EFRX-68N32N0_0123456789ABCDEF-0:0")+"/smart", nil, http.StatusOK, &smart)
	if !smart.Available || smart.DeviceType != "sat" {
		t.Fatalf("smart via sat = %+v", smart)
	}
	for _, id := range []string{"wwn-0xdeadbeef", "-d"} {
		if c := errCode(t, e, "GET", "/api/v1/storage/disks/"+url.PathEscape(id)+"/smart", nil, http.StatusNotFound); c != "storage.disk_not_found" {
			t.Errorf("id %q: %s", id, c)
		}
	}
	// ".." never reaches the handler: the mux cleans the path first.
	e.do("GET", "/api/v1/storage/disks/../smart", nil, http.StatusNotFound, nil)

	var metricsOut struct {
		Current *metrics.Sample
		History []metrics.Sample
	}
	e.do("GET", "/api/v1/system/metrics", nil, http.StatusOK, &metricsOut)
	if metricsOut.Current == nil || len(metricsOut.History) != 1 {
		t.Fatalf("metrics = %+v", metricsOut)
	}
}

func TestIdentity(t *testing.T) {
	e := newEnv(t)
	e.setup()
	if c := errCode(t, e, "PUT", "/api/v1/system/identity", map[string]string{"hostname": "Bad Name", "timezone": "Europe/Paris"}, http.StatusBadRequest); c != "validation.hostname" {
		t.Fatalf("bad hostname: %s", c)
	}
	if c := errCode(t, e, "PUT", "/api/v1/system/identity", map[string]string{"hostname": "nasbuster", "timezone": "Mars/Base"}, http.StatusBadRequest); c != "validation.timezone" {
		t.Fatalf("bad timezone: %s", c)
	}
	e.do("PUT", "/api/v1/system/identity", map[string]string{"hostname": "nasbuster", "timezone": "Asia/Tokyo"}, http.StatusOK, nil)
	var info system.Info
	e.do("GET", "/api/v1/system/info", nil, http.StatusOK, &info)
	if info.Hostname != "nasbuster" || info.Timezone != "Asia/Tokyo" {
		t.Fatalf("identity not applied: %s %s", info.Hostname, info.Timezone)
	}

	var audit struct{ Entries []store.AuditEntry }
	e.do("GET", "/api/v1/audit?limit=10", nil, http.StatusOK, &audit)
	if len(audit.Entries) < 2 || audit.Entries[0].Action != "system.identity" {
		t.Fatalf("audit = %+v", audit.Entries)
	}
}

func TestOnboarding(t *testing.T) {
	e := newEnv(t)
	e.setup()
	var st onboardingState
	e.do("GET", "/api/v1/onboarding", nil, http.StatusOK, &st)
	if st.Completed || st.CurrentStep != "welcome" || st.CompletedSteps == nil {
		t.Fatalf("default = %+v", st)
	}
	want := onboardingState{CurrentStep: "disks", CompletedSteps: []string{"welcome", "health", "account", "identity"}}
	e.do("PUT", "/api/v1/onboarding", want, http.StatusOK, nil)
	e.do("GET", "/api/v1/onboarding", nil, http.StatusOK, &st)
	if st.CurrentStep != "disks" || len(st.CompletedSteps) != 4 {
		t.Fatalf("saved = %+v", st)
	}
	if c := errCode(t, e, "PUT", "/api/v1/onboarding", onboardingState{CurrentStep: "hack"}, http.StatusBadRequest); c != "validation.step" {
		t.Fatalf("bad step: %s", c)
	}
	if c := errCode(t, e, "PUT", "/api/v1/onboarding", map[string]any{"currentStep": "disks", "admin": true}, http.StatusBadRequest); c != "validation.json" {
		t.Fatalf("unknown field: %s", c)
	}
}

func TestEventsStream(t *testing.T) {
	e := newEnv(t)
	e.setup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.srv.URL+"/api/v1/events", nil)
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %s", ct)
	}
	sc := bufio.NewScanner(resp.Body)
	if !sc.Scan() || sc.Text() != "event: metrics" || !sc.Scan() || !strings.HasPrefix(sc.Text(), "data: {") {
		t.Fatalf("first event = %q", sc.Text())
	}
}

func TestSPAAndHeaders(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/", "/setup", "/storage/disks"} {
		resp := e.raw("GET", p, nil, nil, http.StatusOK, nil)
		if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "default-src 'self'") {
			t.Errorf("%s: CSP missing", p)
		}
		if resp.Header.Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: index must not be cached", p)
		}
	}
	resp := e.raw("GET", "/assets/app.js", nil, nil, http.StatusOK, nil)
	if !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Error("assets must be immutable")
	}
	e.raw("GET", "/missing.js", nil, nil, http.StatusNotFound, nil)
	e.raw("GET", "/api/v1/nope", nil, nil, http.StatusNotFound, nil)
}

func TestPoolsFlow(t *testing.T) {
	e := newEnv(t)
	e.setup()
	const sda, sdc, sdd = "wwn-0x5000c500c1234567", "usb-Seagate_ST2000LM015-2E8174_0123456789ABCDEF-0:0", "wwn-0x50014ee2b1234567"

	var layouts struct{ Layouts []layoutView }
	e.do("GET", "/api/v1/storage/layouts", nil, http.StatusOK, &layouts)
	if len(layouts.Layouts) != 5 || layouts.Layouts[1].Name != "mirror" || !layouts.Layouts[1].SelfHealing {
		t.Fatalf("layouts = %+v", layouts)
	}

	// Plan: 4 TB + 4 TB mirror, no side effect.
	var plan storage.Plan
	e.do("POST", "/api/v1/storage/pools/plan", map[string]any{"layout": "mirror", "disks": []string{sda, sdd}}, http.StatusOK, &plan)
	if plan.UsableBytes != storage.PartitionBytes(4000787030016) || plan.Tolerates != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	if c := errCode(t, e, "POST", "/api/v1/storage/pools/plan", map[string]any{"layout": "parity1", "disks": []string{sda, sdd}}, http.StatusBadRequest); c != "validation.disk_count" {
		t.Fatalf("parity with 2 disks: %s", c)
	}
	sdb := "usb-WDC_WD40EFRX-68N32N0_0123456789ABCDEF-0:0" // has data
	if c := errCode(t, e, "POST", "/api/v1/storage/pools/plan", map[string]any{"layout": "mirror", "disks": []string{sda, sdb}}, http.StatusConflict); c != "storage.disk_not_available" {
		t.Fatalf("disk with data: %s", c)
	}

	// Create: confirmation must repeat the name.
	body := map[string]any{"name": "tank", "layout": "parity1", "disks": []string{sda, sdc, sdd}, "confirm": "tonk"}
	if c := errCode(t, e, "POST", "/api/v1/storage/pools", body, http.StatusBadRequest); c != "validation.confirm" {
		t.Fatalf("bad confirm: %s", c)
	}
	body["confirm"] = "tank"
	var created struct{ Pool poolView }
	e.do("POST", "/api/v1/storage/pools", body, http.StatusCreated, &created)
	p := created.Pool
	if p.Name != "tank" || p.Layout != "parity1" || p.MDDevice != "/dev/md/cwnsb-tank" || len(p.Members) != 3 || p.Status == nil {
		t.Fatalf("created = %+v", p)
	}
	if !p.Status.Mounted || p.Status.Health != storage.HealthOK || p.Status.MD == nil || p.Status.MD.Level != "raid5" {
		t.Fatalf("status = %+v md=%+v", p.Status, p.Status.MD)
	}

	// The members are no longer available; a second pool cannot take them.
	var disks struct{ Disks []storage.Disk }
	e.do("GET", "/api/v1/storage/disks", nil, http.StatusOK, &disks)
	for _, d := range disks.Disks {
		if d.Role == storage.RoleAvailable {
			t.Errorf("%s still available after pool creation", d.ID)
		}
	}
	body["name"], body["confirm"] = "other", "other"
	if c := errCode(t, e, "POST", "/api/v1/storage/pools", body, http.StatusConflict); c != "storage.disk_not_available" {
		t.Fatalf("reuse: %s", c)
	}
	body["name"], body["confirm"] = "tank", "tank"
	if c := errCode(t, e, "POST", "/api/v1/storage/pools", body, http.StatusConflict); c != "storage.pool_exists" {
		t.Fatalf("duplicate name: %s", c)
	}

	// List, detail, scrub.
	var list struct{ Pools []poolView }
	e.do("GET", "/api/v1/storage/pools", nil, http.StatusOK, &list)
	if len(list.Pools) != 1 || list.Pools[0].Status == nil || list.Pools[0].Status.Scrub.Status != "never" {
		t.Fatalf("list = %+v", list)
	}
	start := time.Now()
	e.sys.Clock = func() time.Time { return start }
	e.do("POST", "/api/v1/storage/pools/tank/scrub", nil, http.StatusAccepted, nil)
	var one struct{ Pool poolView }
	e.do("GET", "/api/v1/storage/pools/tank", nil, http.StatusOK, &one)
	if one.Pool.Status.Scrub.Status != "running" {
		t.Fatalf("scrub just started = %+v", one.Pool.Status.Scrub)
	}
	e.advance(2 * time.Minute)
	e.do("GET", "/api/v1/storage/pools/tank", nil, http.StatusOK, &one)
	if one.Pool.Status.Scrub.Status != "finished" {
		t.Fatalf("after scrub = %+v", one.Pool.Status.Scrub)
	}
	if c := errCode(t, e, "GET", "/api/v1/storage/pools/nope", nil, http.StatusNotFound); c != "storage.pool_not_found" {
		t.Fatalf("missing pool: %s", c)
	}

	var audit struct{ Entries []store.AuditEntry }
	e.do("GET", "/api/v1/audit?limit=5", nil, http.StatusOK, &audit)
	if audit.Entries[0].Action != "pool.scrub" || audit.Entries[1].Action != "pool.create" {
		t.Fatalf("audit = %+v", audit.Entries[:2])
	}
}

func TestSharesFlow(t *testing.T) {
	e := newEnv(t)
	e.setup()
	e.do("POST", "/api/v1/storage/pools", map[string]any{"name": "tank", "layout": "mirror",
		"disks": []string{"wwn-0x5000c500c1234567", "wwn-0x50014ee2b1234567"}, "confirm": "tank"}, http.StatusCreated, nil)

	const pw = "correct horse battery"
	e.do("POST", "/api/v1/users", map[string]string{"name": "alice", "password": pw}, http.StatusCreated, nil)
	e.do("POST", "/api/v1/users", map[string]string{"name": "bob", "password": pw}, http.StatusCreated, nil)
	if c := errCode(t, e, "POST", "/api/v1/users", map[string]string{"name": "alice", "password": pw}, http.StatusConflict); c != "users.exists" {
		t.Fatalf("duplicate user: %s", c)
	}
	for _, bad := range []map[string]string{{"name": "root", "password": pw}, {"name": "Bob", "password": pw}} {
		if c := errCode(t, e, "POST", "/api/v1/users", bad, http.StatusBadRequest); c != "validation.user_name" {
			t.Errorf("%v: %s", bad, c)
		}
	}
	if c := errCode(t, e, "POST", "/api/v1/users", map[string]string{"name": "carol", "password": "short"}, http.StatusBadRequest); c != "validation.user_password" {
		t.Fatalf("short password: %s", c)
	}

	access := []map[string]string{{"user": "alice", "mode": "rw"}, {"user": "bob", "mode": "ro"}}
	var created struct{ Share shareView }
	e.do("POST", "/api/v1/shares", map[string]any{"name": "photos", "pool": "tank", "access": access}, http.StatusCreated, &created)
	if created.Share.Path != "/srv/cwnsb/tank/photos" || len(created.Share.Access) != 2 {
		t.Fatalf("share = %+v", created.Share)
	}
	conf, _ := e.sys.Files.ReadFile("/etc/samba/smb.conf")
	if !strings.Contains(string(conf), "valid users = alice bob") || !strings.Contains(string(conf), "write list = alice") {
		t.Fatalf("smb.conf = %s", conf)
	}
	cases := []struct {
		body   map[string]any
		status int
		code   string
	}{
		{map[string]any{"name": "photos", "pool": "tank"}, http.StatusConflict, "shares.exists"},
		{map[string]any{"name": "films", "pool": "nope"}, http.StatusNotFound, "storage.pool_not_found"},
		{map[string]any{"name": "films", "pool": "tank", "access": []map[string]string{{"user": "carol", "mode": "rw"}}}, http.StatusNotFound, "users.not_found"},
		{map[string]any{"name": "films", "pool": "tank", "access": []map[string]string{{"user": "alice", "mode": "owner"}}}, http.StatusBadRequest, "validation.access_mode"},
		{map[string]any{"name": "global", "pool": "tank"}, http.StatusBadRequest, "validation.share_name"},
	}
	for _, c := range cases {
		if got := errCode(t, e, "POST", "/api/v1/shares", c.body, c.status); got != c.code {
			t.Errorf("%v: %s, want %s", c.body, got, c.code)
		}
	}

	e.do("PUT", "/api/v1/shares/photos/access", map[string]any{"access": []map[string]string{{"user": "alice", "mode": "rw"}}}, http.StatusOK, nil)
	var list struct{ Shares []shareView }
	e.do("GET", "/api/v1/shares", nil, http.StatusOK, &list)
	if len(list.Shares) != 1 || len(list.Shares[0].Access) != 1 {
		t.Fatalf("shares = %+v", list.Shares)
	}

	// The onboarding's "connection detected": nobody yet, then a PC connects.
	var sessions helper.SMBSessionsResult
	e.do("GET", "/api/v1/shares/sessions", nil, http.StatusOK, &sessions)
	if len(sessions.Sessions) != 0 {
		t.Fatalf("early sessions = %+v", sessions)
	}
	later := time.Now().Add(10 * time.Second)
	e.sys.Clock = func() time.Time { return later }
	e.do("GET", "/api/v1/shares/sessions", nil, http.StatusOK, &sessions)
	if len(sessions.Sessions) != 1 || sessions.Sessions[0].Machine != "DESKTOP-7Q2K" || sessions.Connections[0].Share != "photos" {
		t.Fatalf("sessions = %+v", sessions)
	}

	e.do("PUT", "/api/v1/users/alice/password", map[string]string{"password": "another long one"}, http.StatusNoContent, nil)
	e.do("DELETE", "/api/v1/users/bob", nil, http.StatusNoContent, nil)
	var users struct{ Users []store.NASUser }
	e.do("GET", "/api/v1/users", nil, http.StatusOK, &users)
	if len(users.Users) != 1 || users.Users[0].Name != "alice" || len(users.Users[0].Shares) != 1 {
		t.Fatalf("users = %+v", users.Users)
	}
	if c := errCode(t, e, "DELETE", "/api/v1/users/root", nil, http.StatusNotFound); c != "users.not_found" {
		t.Fatalf("delete root: %s", c)
	}

	var audit struct{ Entries []store.AuditEntry }
	e.do("GET", "/api/v1/audit?limit=50", nil, http.StatusOK, &audit)
	for _, en := range audit.Entries {
		if strings.Contains(en.Detail, "horse") || strings.Contains(en.Target, "horse") {
			t.Fatalf("password in audit log: %+v", en)
		}
	}
}

func TestProtectionAndSnapshots(t *testing.T) {
	e := newEnv(t)
	e.setup()
	e.do("POST", "/api/v1/storage/pools", map[string]any{"name": "tank", "layout": "mirror",
		"disks": []string{"wwn-0x5000c500c1234567", "wwn-0x50014ee2b1234567"}, "confirm": "tank"}, http.StatusCreated, nil)
	e.do("POST", "/api/v1/shares", map[string]any{"name": "photos", "pool": "tank"}, http.StatusCreated, nil)

	var prot struct {
		Protection struct {
			Snapshots struct {
				Enabled bool
				Policy  struct{ Hourly, Daily, Weekly, Monthly int }
			}
			Scrub struct {
				Enabled      bool
				IntervalDays int
			}
		}
	}
	e.do("GET", "/api/v1/protection", nil, http.StatusOK, &prot)
	if !prot.Protection.Snapshots.Enabled || prot.Protection.Snapshots.Policy.Hourly != 24 || prot.Protection.Scrub.IntervalDays != 30 {
		t.Fatalf("defaults = %+v", prot)
	}
	bad := map[string]any{"snapshots": map[string]any{"enabled": true, "policy": map[string]int{"hourly": 0}}, "scrub": map[string]any{"enabled": true, "intervalDays": 1}}
	if c := errCode(t, e, "PUT", "/api/v1/protection", bad, http.StatusBadRequest); c != "validation.protection" {
		t.Fatalf("bad policy: %s", c)
	}
	good := map[string]any{"snapshots": map[string]any{"enabled": true, "policy": map[string]int{"hourly": 12, "daily": 7, "weekly": 4, "monthly": 3}}, "scrub": map[string]any{"enabled": true, "intervalDays": 14}}
	e.do("PUT", "/api/v1/protection", good, http.StatusOK, nil)

	var created struct{ Snapshot snapshotView }
	e.do("POST", "/api/v1/shares/photos/snapshots", nil, http.StatusCreated, &created)
	if !strings.HasPrefix(created.Snapshot.Name, "@GMT-") || created.Snapshot.Time == 0 {
		t.Fatalf("snapshot = %+v", created.Snapshot)
	}
	var list struct{ Snapshots []snapshotView }
	e.do("GET", "/api/v1/shares/photos/snapshots", nil, http.StatusOK, &list)
	if len(list.Snapshots) != 1 || list.Snapshots[0].Name != created.Snapshot.Name {
		t.Fatalf("list = %+v", list)
	}
	e.do("DELETE", "/api/v1/shares/photos/snapshots/"+url.PathEscape(created.Snapshot.Name), nil, http.StatusNoContent, nil)
	if c := errCode(t, e, "DELETE", "/api/v1/shares/photos/snapshots/not-a-snapshot", nil, http.StatusNotFound); c != "snapshots.not_found" {
		t.Fatalf("bad name: %s", c)
	}
	if c := errCode(t, e, "GET", "/api/v1/shares/nope/snapshots", nil, http.StatusNotFound); c != "shares.not_found" {
		t.Fatalf("unknown share: %s", c)
	}
}

func TestAlertsAndNotificationsAPI(t *testing.T) {
	e := newEnv(t)
	e.setup()
	var received []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		received = append(received, r.Header.Get("Authorization")+"|"+string(b))
	}))
	defer srv.Close()

	var view notificationsView
	e.do("GET", "/api/v1/notifications", nil, http.StatusOK, &view)
	if view.Ntfy.Enabled || view.Ntfy.TokenSet {
		t.Fatalf("defaults = %+v", view)
	}
	bad := map[string]any{"ntfy": map[string]any{"enabled": true, "url": "http://ntfy.sh/topic"}}
	if c := errCode(t, e, "PUT", "/api/v1/notifications", bad, http.StatusBadRequest); c != "validation.url" {
		t.Fatalf("plain http to the internet: %s", c)
	}
	body := map[string]any{"ntfy": map[string]any{"enabled": true, "url": srv.URL + "/nas", "token": "tk_secret"}}
	e.do("PUT", "/api/v1/notifications", body, http.StatusOK, &view)
	if !view.Ntfy.TokenSet {
		t.Fatalf("token not recorded: %+v", view)
	}
	// The token is never sent back.
	var raw map[string]any
	e.do("GET", "/api/v1/notifications", nil, http.StatusOK, &raw)
	if b, _ := json.Marshal(raw); strings.Contains(string(b), "tk_secret") {
		t.Fatalf("secret leaked: %s", b)
	}
	// Updating without "token" keeps it.
	e.do("PUT", "/api/v1/notifications", map[string]any{"ntfy": map[string]any{"enabled": true, "url": srv.URL + "/nas"}}, http.StatusOK, &view)
	if !view.Ntfy.TokenSet {
		t.Fatal("token lost on update without token")
	}

	var test struct{ Results []alerts.Result }
	e.do("POST", "/api/v1/notifications/test", nil, http.StatusOK, &test)
	if len(test.Results) != 1 || !test.Results[0].OK || len(received) != 1 || !strings.HasPrefix(received[0], "Bearer tk_secret|") || !strings.Contains(received[0], "CrossWax NasBuster") {
		t.Fatalf("test = %+v received = %q", test, received)
	}

	// Demo mode: an Internet target is never contacted.
	e.do("PUT", "/api/v1/notifications", map[string]any{"ntfy": map[string]any{"enabled": true, "url": "https://ntfy.sh/cwnsb-demo-topic"}}, http.StatusOK, nil)
	e.do("POST", "/api/v1/notifications/test", nil, http.StatusOK, &test)
	if len(test.Results) != 1 || test.Results[0].Code != "notify.demo" || len(received) != 1 {
		t.Fatalf("demo test to the Internet = %+v (received %d)", test, len(received))
	}
	var badURL struct{ Error apiError }
	e.do("PUT", "/api/v1/notifications", map[string]any{"webhook": map[string]any{"enabled": true, "url": "http://example.com/h"}}, http.StatusBadRequest, &badURL)
	if badURL.Error.Params["channel"] != "webhook" {
		t.Fatalf("validation.url params = %+v", badURL.Error)
	}

	var list struct{ Alerts []store.Alert }
	e.do("GET", "/api/v1/alerts", nil, http.StatusOK, &list)
	if len(list.Alerts) != 0 {
		t.Fatalf("alerts = %+v", list)
	}
	if c := errCode(t, e, "POST", "/api/v1/alerts/"+url.PathEscape("pool.degraded:tank")+"/ack", nil, http.StatusNotFound); c != "alerts.not_found" {
		t.Fatalf("ack unknown: %s", c)
	}
}
