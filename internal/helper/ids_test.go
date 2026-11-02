// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/crosswax/nasbuster/internal/backup"
	"github.com/crosswax/nasbuster/internal/shares"
)

// newIDsEnv: a fresh system (first account 1000, cwnsb-users made by Debian's
// range) with the pool "tank" imported, carrying a backup of the installation
// before: alice (1001) and the share group of "lanparty" (1002).
func newIDsEnv(t *testing.T) *shareEnv {
	t.Helper()
	e := newShareEnv(t)
	e.ops.UnitDir = "/etc/systemd/system"
	e.files.WriteFile("/etc/systemd/system/srv-cwnsb-tank.mount", []byte("[Mount]\n"), 0)
	e.ops.FS = fstest.MapFS{
		"etc/passwd": {Data: []byte("root:x:0:0:root:/root:/bin/bash\ncrosswax:x:1000:1000::/home/crosswax:/bin/bash\nnobody:x:65534:65534::/nonexistent:/usr/sbin/nologin\n")},
		"etc/group":  {Data: []byte("root:x:0:\ncrosswax:x:1000:\nsudo:x:27:crosswax\ncwnsb-users:x:983:\nnogroup:x:65534:\n")},
	}
	m, _ := json.Marshal(backup.Manifest{Version: 2, Installation: "fedcba9876543210fedcba9876543210",
		Users:  []backup.User{{Name: "alice", UID: 1001, GID: 1001}},
		Groups: []backup.Group{{Name: "alice", GID: 1001}, {Name: "cwnsb-share-lanparty", GID: 1002}, {Name: shares.UsersGroup, GID: 981}}})
	dir := backup.Dir("tank") + "/2026-10-29T00-29-16Z"
	e.files.MkdirAll(dir, 0)
	e.files.WriteFile(dir+"/"+backup.ManifestFile, m, 0)
	delete(e.users, "alice") // a fresh SD card: the old accounts are gone
	return e
}

// numbers lists the --gid / --uid values given to groupadd and useradd.
func (e *shareEnv) numbers(prog, flag string) []string {
	var out []string
	for _, c := range e.ran(prog) {
		if i := slices.Index(c, flag); i >= 0 && i+1 < len(c) {
			out = append(out, c[i+1]+" "+c[len(c)-1])
		}
	}
	return out
}

// P3: on a fresh SD card with an imported pool, a new share group and a new
// user never take a number that owned files on the pool, and a number is
// never given twice, not even after its account is deleted.
func TestNewAccountsNeverTakeANumberThePoolKnows(t *testing.T) {
	e := newIDsEnv(t)
	ctx := context.Background()

	if err := e.ops.ensureGroup(ctx, shares.ShareGroup("documents")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ops.userCreate(ctx, UserArgs{Name: "nasbuster", Password: "correct horse"}); err != nil {
		t.Fatal(err)
	}
	if got := e.numbers("groupadd", "--gid"); !slices.Equal(got, []string{"1003 cwnsb-share-documents", "1004 nasbuster"}) {
		t.Fatalf("groupadd = %v (1001 and 1002 own files on tank)", got)
	}
	if got := e.numbers("useradd", "--uid"); !slices.Equal(got, []string{"1004 nasbuster"}) {
		t.Fatalf("useradd = %v", got)
	}

	// The pool remembers: deleting the account frees nothing.
	if _, err := e.ops.userDelete(ctx, UserArgs{Name: "nasbuster"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ops.userCreate(ctx, UserArgs{Name: "bob", Password: "correct horse"}); err != nil {
		t.Fatal(err)
	}
	if got := e.numbers("useradd", "--uid"); got[len(got)-1] != "1005 bob" {
		t.Fatalf("useradd = %v (1004 was nasbuster's)", got)
	}
	var l idLedger
	b, _ := e.files.ReadFile(idsPath("tank"))
	if err := json.Unmarshal(b, &l); err != nil || !slices.Equal(l.UIDs, []int{1004, 1005}) || !slices.Equal(l.GIDs, []int{1003, 1004, 1005}) {
		t.Fatalf("ledger = %s", b)
	}
}

// A ledger that cannot be read could hide a number in use: refuse.
func TestUnreadableLedgerRefusesNewAccounts(t *testing.T) {
	e := newIDsEnv(t)
	e.files.WriteFile(idsPath("tank"), []byte("{not json"), 0)
	if _, err := e.ops.userCreate(context.Background(), UserArgs{Name: "bob", Password: "correct horse"}); err == nil || err.Code != CodeFailed {
		t.Fatalf("err = %v", err)
	}
	if len(e.ran("useradd")) != 0 {
		t.Fatalf("useradd ran: %v", e.ran("useradd"))
	}
}
