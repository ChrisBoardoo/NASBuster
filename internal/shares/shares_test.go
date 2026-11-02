// SPDX-License-Identifier: AGPL-3.0-or-later

package shares

import (
	"os"
	"strings"
	"testing"
)

func TestNames(t *testing.T) {
	for _, ok := range []string{"photos", "family-docs", "tv_shows", "a", "2026"} {
		if !ValidShareName(ok) {
			t.Errorf("share %q rejected", ok)
		}
	}
	for _, bad := range []string{"", "Photos", "global", "homes", "ipc$", "my share", "a/b", "../x", "-x", strings.Repeat("a", 21), "x\n[global]"} {
		if ValidShareName(bad) {
			t.Errorf("share %q accepted", bad)
		}
	}
	for _, ok := range []string{"alice", "bob-2", "m_dupont"} {
		if !ValidUserName(ok) {
			t.Errorf("user %q rejected", ok)
		}
	}
	for _, bad := range []string{"root", "nobody", "cwnsb", "admin", "Alice", "a", "1bob", "bob smith", "bob\nroot"} {
		if ValidUserName(bad) {
			t.Errorf("user %q accepted", bad)
		}
	}
	if !ValidPassword("correct horse") || ValidPassword("short") || ValidPassword("two\nlines!") {
		t.Error("password rules")
	}
	if g := ShareGroup(strings.Repeat("a", 20)); len(g) > 32 {
		t.Errorf("group %q exceeds Linux's 32-char limit", g)
	}
}

func TestRender(t *testing.T) {
	conf, err := Render([]Share{
		{Name: "photos", Pool: "tank", Access: []Access{{User: "bob", Mode: ReadOnly}, {User: "alice", Mode: ReadWrite}}},
		{Name: "archive", Pool: "tank"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		Header,
		"map to guest = never",
		"server min protocol = SMB2_10",
		"[archive]\n   path = /srv/cwnsb/tank/archive\n   valid users = @cwnsb-users\n   invalid users = @cwnsb-users\n",
		"[photos]\n   path = /srv/cwnsb/tank/photos\n   valid users = alice bob\n   read only = yes\n   write list = alice\n   force group = cwnsb-share-photos\n",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("smb.conf lacks:\n%s\n--- got ---\n%s", want, conf)
		}
	}
	if strings.Index(conf, "[archive]") > strings.Index(conf, "[photos]") {
		t.Error("shares must be sorted")
	}
	for _, forbidden := range []string{"guest ok", "force user", "root", "admin users"} {
		if strings.Contains(conf, forbidden) {
			t.Errorf("smb.conf must never contain %q", forbidden)
		}
	}
}

func TestRenderRejectsInjection(t *testing.T) {
	bad := []Share{
		{Name: "x\n[global]\nforce user = root", Pool: "tank"},
		{Name: "ok", Pool: "../etc"},
		{Name: "ok", Pool: "tank", Access: []Access{{User: "root", Mode: ReadWrite}}},
		{Name: "ok", Pool: "tank", Access: []Access{{User: "alice", Mode: "admin"}}},
		{Name: "ok", Pool: "tank", Access: []Access{{User: "alice", Mode: "rw"}, {User: "alice", Mode: "ro"}}},
	}
	for _, s := range bad {
		if _, err := Render([]Share{s}); err == nil {
			t.Errorf("Render accepted %+v", s)
		}
	}
	if _, err := Render([]Share{{Name: "a", Pool: "tank"}, {Name: "a", Pool: "data"}}); err != ErrDuplicateShare {
		t.Errorf("duplicate: %v", err)
	}
}

func TestParseSmbstatus(t *testing.T) {
	b, err := os.ReadFile("testdata/smbstatus.json") // real Samba 4.22 output from the lab
	if err != nil {
		t.Fatal(err)
	}
	sessions, conns, err := ParseSmbstatus(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].User != "probeuser" || sessions[0].Dialect != "SMB3_11" ||
		sessions[0].Address != "127.0.0.1:36380" || sessions[0].Encrypted {
		t.Fatalf("sessions = %+v", sessions)
	}
	if len(conns) != 1 || conns[0].Share != "probe" || conns[0].Machine != "127.0.0.1" {
		t.Fatalf("connections = %+v", conns)
	}
}

func TestRenderTimeMachine(t *testing.T) {
	conf, err := Render([]Share{
		{Name: "backup-mac", Pool: "tank", Access: []Access{{User: "alice", Mode: ReadWrite}}, TimeMachine: true, TimeMachineMaxGiB: 500},
		{Name: "photos", Pool: "tank"},
	})
	if err != nil {
		t.Fatal(err)
	}
	i, j := strings.Index(conf, "[backup-mac]"), strings.Index(conf, "[photos]")
	tm, photos := conf[i:j], conf[j:]
	if !strings.Contains(tm, "fruit:time machine = yes\n") || !strings.Contains(tm, "fruit:time machine max size = 500G\n") {
		t.Fatalf("time machine share:\n%s", tm)
	}
	if strings.Contains(photos, "time machine") {
		t.Fatal("time machine leaked into another share")
	}
	if !strings.Contains(conf, "fruit:model = RackMac") {
		t.Fatal("Finder icon")
	}
	if got := TimeMachineShares(conf); len(got) != 1 || got[0] != "backup-mac" {
		t.Fatalf("TimeMachineShares = %v", got)
	}
	for _, bad := range []Share{
		{Name: "x", Pool: "tank", TimeMachineMaxGiB: 10}, // limit without Time Machine
		{Name: "x", Pool: "tank", TimeMachine: true, TimeMachineMaxGiB: -1},
		{Name: "x", Pool: "tank", TimeMachine: true, TimeMachineMaxGiB: MaxTimeMachineGiB + 1},
	} {
		if bad.Validate() == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestRenderRecycleBin(t *testing.T) {
	conf, err := Render([]Share{{Name: "docs", Pool: "tank", Recycle: true, RecycleDays: 30}, {Name: "media", Pool: "tank"}})
	if err != nil {
		t.Fatal(err)
	}
	i, j := strings.Index(conf, "[docs]"), strings.Index(conf, "[media]")
	docs, media := conf[i:j], conf[j:]
	for _, want := range []string{"vfs objects = catia fruit streams_xattr shadow_copy2 recycle\n", "recycle:repository = #recycle\n",
		"recycle:touch_mtime = yes\n", "recycle:exclude_dir = .snapshots,#recycle\n"} {
		if !strings.Contains(docs, want) {
			t.Errorf("docs lacks %q", want)
		}
	}
	if strings.Contains(media, "recycle") || !strings.Contains(media, "vfs objects = catia fruit streams_xattr shadow_copy2\n") {
		t.Fatalf("media:\n%s", media)
	}
	for _, bad := range []Share{{Name: "x", Pool: "tank", Recycle: true}, {Name: "x", Pool: "tank", RecycleDays: 5},
		{Name: "x", Pool: "tank", Recycle: true, RecycleDays: 366}} {
		if bad.Validate() == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}
