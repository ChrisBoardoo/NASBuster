// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/crosswax/nasbuster/internal/demo"
	"github.com/crosswax/nasbuster/internal/storage"
	"github.com/crosswax/nasbuster/internal/sysexec"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func demoOps() (*Ops, *demo.System, map[string][]byte) {
	sys := demo.New()
	written := map[string][]byte{}
	ops := &Ops{
		Run:          sys.Runner,
		FS:           sys.FS,
		ByIDDir:      "/dev/disk/by-id",
		DeviceExists: sys.DeviceExists,
		WriteFile: func(path string, data []byte, _ fs.FileMode) error {
			written[path] = data
			return nil
		},
		Logger: quietLogger(),
	}
	return ops, sys, written
}

func call(t *testing.T, ops *Ops, op string, args any, result any) error {
	t.Helper()
	return (&LocalClient{Ops: ops}).Call(context.Background(), op, args, result)
}

func codeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestSmartHealthyDisk(t *testing.T) {
	ops, _, _ := demoOps()
	var r storage.SmartReport
	if err := call(t, ops, OpDiskSmart, DiskSmartArgs{ID: "wwn-0x5000c500c1234567"}, &r); err != nil {
		t.Fatal(err)
	}
	if !r.Available || r.Passed == nil || !*r.Passed || len(r.Attributes) != 5 {
		t.Fatalf("report = %+v", r)
	}
}

func TestSmartRetriesWithSAT(t *testing.T) {
	ops, sys, _ := demoOps()
	var r storage.SmartReport
	if err := call(t, ops, OpDiskSmart, DiskSmartArgs{ID: "usb-WDC_WD40EFRX-68N32N0_0123456789ABCDEF-0:0"}, &r); err != nil {
		t.Fatal(err)
	}
	if !r.Available || r.DeviceType != "sat" {
		t.Fatalf("report = %+v", r)
	}
	crc := r.Attributes[len(r.Attributes)-1]
	if crc.ID != 199 || crc.Raw != 3 || crc.Status != "warn" {
		t.Errorf("CRC attribute = %+v", crc)
	}
	calls := sys.Runner.Calls()
	last := calls[len(calls)-1]
	if !slices.Contains(last, "sat") {
		t.Errorf("last call %v should force -d sat", last)
	}
}

func TestSmartUnsupportedBridgeKeepsReason(t *testing.T) {
	ops, _, _ := demoOps()
	var r storage.SmartReport
	if err := call(t, ops, OpDiskSmart, DiskSmartArgs{ID: "usb-Seagate_ST2000LM015-2E8174_0123456789ABCDEF-0:0"}, &r); err != nil {
		t.Fatal(err)
	}
	if r.Available || r.Reason != storage.SmartUSBBridgeUnsupported {
		t.Fatalf("report = %+v", r)
	}
}

func TestSmartRejectsUnsafeIDs(t *testing.T) {
	ops, sys, _ := demoOps()
	for _, id := range []string{"", "..", "../../sda", "-d", "wwn;reboot", "a/b"} {
		err := call(t, ops, OpDiskSmart, DiskSmartArgs{ID: id}, nil)
		if codeOf(err) != CodeInvalidArgs {
			t.Errorf("id %q: err = %v, want %s", id, err, CodeInvalidArgs)
		}
	}
	if err := call(t, ops, OpDiskSmart, DiskSmartArgs{ID: "wwn-0xdeadbeef"}, nil); codeOf(err) != CodeDiskNotFound {
		t.Errorf("unknown disk: err = %v", err)
	}
	for _, c := range sys.Runner.Calls() {
		if c[0] == "smartctl" {
			t.Errorf("smartctl must not run for rejected ids, ran %v", c)
		}
	}
}

func TestSmartNotInstalled(t *testing.T) {
	ops, _, _ := demoOps()
	ops.Run = sysexec.NewFake() // nothing registered: ErrNotFound
	var r storage.SmartReport
	if err := call(t, ops, OpDiskSmart, DiskSmartArgs{ID: "wwn-0x5000c500c1234567"}, &r); err != nil {
		t.Fatal(err)
	}
	if r.Available || r.Reason != storage.SmartNotInstalled {
		t.Fatalf("report = %+v", r)
	}
}

func TestSetHostname(t *testing.T) {
	ops, sys, written := demoOps()
	if err := call(t, ops, OpSetHostname, SetHostnameArgs{Hostname: "nasbuster"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := string(written["/etc/hosts"]); got != "127.0.0.1\tlocalhost\n::1\t\tlocalhost ip6-localhost ip6-loopback\n127.0.1.1\tnasbuster\n" {
		t.Errorf("/etc/hosts = %q", got)
	}
	if h, _ := fs.ReadFile(sys.FS, "proc/sys/kernel/hostname"); string(h) != "nasbuster\n" {
		t.Errorf("hostname = %q", h)
	}
	if err := call(t, ops, OpSetHostname, SetHostnameArgs{Hostname: "bad name"}, nil); codeOf(err) != CodeHostname {
		t.Errorf("err = %v", err)
	}
}

func TestUpdateHostsAddsMissingLine(t *testing.T) {
	got := updateHosts("127.0.0.1 localhost\n::1 localhost\n", "nas")
	if got != "127.0.0.1 localhost\n127.0.1.1\tnas\n::1 localhost\n" {
		t.Fatalf("updateHosts = %q", got)
	}
}

func TestSetTimezone(t *testing.T) {
	ops, sys, _ := demoOps()
	if err := call(t, ops, OpSetTimezone, SetTimezoneArgs{Timezone: "Asia/Tokyo"}, nil); err != nil {
		t.Fatal(err)
	}
	if tz, _ := fs.ReadFile(sys.FS, "etc/timezone"); string(tz) != "Asia/Tokyo\n" {
		t.Errorf("timezone = %q", tz)
	}
	for _, tz := range []string{"Mars/Olympus", "../../etc/passwd"} {
		if err := call(t, ops, OpSetTimezone, SetTimezoneArgs{Timezone: tz}, nil); codeOf(err) != CodeTimezone {
			t.Errorf("%q: err = %v", tz, err)
		}
	}
}

func TestDispatchRejectsUnknownOpAndFields(t *testing.T) {
	ops, _, _ := demoOps()
	if _, err := ops.Dispatch(context.Background(), Request{Op: "exec"}); err == nil || err.Code != CodeUnknownOp {
		t.Errorf("unknown op: %v", err)
	}
	_, err := ops.Dispatch(context.Background(), Request{Op: OpSetHostname, Args: json.RawMessage(`{"hostname":"nas","command":"x"}`)})
	if err == nil || err.Code != CodeInvalidArgs {
		t.Errorf("unknown field: %v", err)
	}
}

// serveOne runs the server on one end of a pipe with a fixed caller uid.
func serveOne(t *testing.T, uid uint32, request string) Response {
	t.Helper()
	ops, _, _ := demoOps()
	srv := &Server{Ops: ops, AllowedUIDs: map[uint32]bool{999: true}, Logger: quietLogger(),
		PeerUID: func(net.Conn) (uint32, error) { return uid, nil }}
	client, server := net.Pipe()
	defer client.Close()
	go srv.handle(context.Background(), server)
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	// net.Pipe is unbuffered (unlike a Unix socket): a rejected caller gets its
	// reply before the server reads anything, so write concurrently.
	go func() { _, _ = client.Write([]byte(request + "\n")) }()
	line, err := bufio.NewReader(client).ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestServerChecksCaller(t *testing.T) {
	if resp := serveOne(t, 1000, `{"op":"ping"}`); resp.OK || resp.Error.Code != CodeForbidden {
		t.Errorf("uid 1000: %+v", resp)
	}
	for _, uid := range []uint32{0, 999} {
		if resp := serveOne(t, uid, `{"op":"ping"}`); !resp.OK {
			t.Errorf("uid %d: %+v", uid, resp)
		}
	}
}

func TestServerRejectsMalformed(t *testing.T) {
	if resp := serveOne(t, 0, `{"op":"ping","extra":1}`); resp.OK || resp.Error.Code != CodeInvalidRequest {
		t.Errorf("extra field: %+v", resp)
	}
	if resp := serveOne(t, 0, `not json`); resp.OK || resp.Error.Code != CodeInvalidRequest {
		t.Errorf("garbage: %+v", resp)
	}
}
