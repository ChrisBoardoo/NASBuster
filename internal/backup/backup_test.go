// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestNamesAndPrune(t *testing.T) {
	n := Name(time.Date(2026, 10, 2, 1, 30, 5, 0, time.UTC))
	if n != "2026-10-02T01-30-05Z" || !ValidName(n) || ValidName("../x") || ValidName("2026-10-02") {
		t.Fatal(n)
	}
	var names []string
	for d := 1; d <= 10; d++ {
		names = append(names, Name(time.Date(2026, 9, d, 0, 0, 0, 0, time.UTC)))
	}
	const mine, theirs = "0123456789abcdef0123456789abcdef", "fedcba9876543210fedcba9876543210"
	var entries []Entry
	for _, n := range names {
		entries = append(entries, Entry{Name: n, Installation: mine})
	}
	// Another installation's backups, and older ones without an id, are
	// never rotated by this one, however many there are or however old.
	for d := 1; d <= 9; d++ {
		entries = append(entries, Entry{Name: Name(time.Date(2026, 8, d, 0, 0, 0, 0, time.UTC)), Installation: theirs})
	}
	entries = append(entries, Entry{Name: Name(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))}, Entry{Name: "notabackup", Installation: mine})
	old := PruneFor(entries, mine)
	if len(old) != 3 || old[0] != Name(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("prune = %v", old)
	}
	// A fresh installation with two backups deletes nothing at all.
	if old := PruneFor(append(entries, Entry{Name: Name(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)), Installation: "abcdefabcdefabcdefabcdefabcdefab"}), "abcdefabcdefabcdefabcdefabcdefab"); old != nil {
		t.Fatalf("fresh installation prunes %v", old)
	}
	// Without a valid installation id, nothing is ever deleted.
	if old := PruneFor(entries, ""); old != nil {
		t.Fatalf("no id prunes %v", old)
	}
	// Every manifest written so far can be restored (version 2 was refused
	// at first: caught by TestLabConfigBackupRestore).
	if !SupportedVersion(1) || !SupportedVersion(ManifestVersion) || SupportedVersion(0) || SupportedVersion(ManifestVersion+1) {
		t.Fatal("supported manifest versions")
	}
	if Dir("tank") != "/srv/cwnsb/tank/.cwnsb/backup" || !IsSQLite([]byte("SQLite format 3\x00rest")) || IsSQLite([]byte("root:x:0:0")) {
		t.Fatal("paths or header")
	}
}

func TestIdentities(t *testing.T) {
	passwd := "root:x:0:0:root:/root:/bin/bash\nadmin:x:1000:1000::/home/admin:/bin/bash\nalice:x:1001:1001:CrossWax NasBuster user:/nonexistent:/usr/sbin/nologin\nbob:x:1002:1002:CrossWax NasBuster user:/nonexistent:/usr/sbin/nologin\n"
	group := "root:x:0:\nadmin:x:1000:\nalice:x:1001:\nbob:x:1002:\ncwnsb-users:x:990:alice,bob\ncwnsb-share-photos:x:989:\nsudo:x:27:admin\n"
	users, groups := Identities([]byte(passwd), []byte(group))
	if len(users) != 2 || users[0] != (User{Name: "alice", UID: 1001, GID: 1001}) {
		t.Fatalf("users = %+v", users)
	}
	var names []string
	for _, g := range groups {
		names = append(names, g.Name)
	}
	if !slices.Equal(names, []string{"alice", "bob", "cwnsb-share-photos", "cwnsb-users"}) {
		t.Fatalf("groups = %v (the admin account and system groups must not be included)", names)
	}
	samba := FilterSamba([]byte("alice:1001:XXX:HASHA:[U ]:LCT-1:\nroot:0:XXX:HASHR:[U ]:LCT-1:\nbob:1002:XXX:HASHB:[U ]:LCT-1:\n"), users)
	if strings.Contains(string(samba), "root") || strings.Count(string(samba), "\n") != 2 {
		t.Fatalf("samba = %q", samba)
	}
}
