// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/crosswax/nasbuster/internal/sysexec"
)

// Debian's own journald.conf: every setting commented out.
const debianJournaldConf = "[Journal]\n#Storage=auto\n#Compress=yes\n#SystemMaxUse=\n"

func newJournaldOps(files *MemFiles) (*Ops, *sysexec.Fake) {
	run := sysexec.NewFake()
	for _, cmd := range []string{"systemctl", "systemd-tmpfiles", "journalctl"} {
		run.Handle(cmd, func([]string) (sysexec.Result, error) { return sysexec.Result{}, nil })
	}
	return &Ops{Run: run, Files: files, Logger: quietLogger()}, run
}

func cmds(run *sysexec.Fake) []string {
	var out []string
	for _, c := range run.Calls() {
		out = append(out, strings.Join(c, " "))
	}
	return out
}

func TestJournalPersistOnAndOff(t *testing.T) {
	files := NewMemFiles()
	files.WriteFile(journaldConf, []byte(debianJournaldConf), 0o644)
	o, run := newJournaldOps(files)
	ctx := context.Background()

	res, err := o.journalPersist(ctx, JournalPersistArgs{Enabled: true})
	if err != nil || !res.Persistent || !res.Changed || res.ManagedBy != "" {
		t.Fatalf("on = %+v %v", res, err)
	}
	b, _ := files.ReadFile(JournaldDropIn)
	if !strings.Contains(string(b), "Storage=persistent\nSystemMaxUse=64M\n") {
		t.Fatalf("drop-in = %q", b)
	}
	if got := cmds(run); !slices.Equal(got, []string{"systemd-tmpfiles --create --prefix /var/log/journal",
		"systemctl restart systemd-journald.service", "journalctl --flush"}) {
		t.Fatalf("commands = %q", got)
	}

	// Already on (every daemon start): nothing written, nothing restarted.
	o, run = newJournaldOps(files)
	if res, _ := o.journalPersist(ctx, JournalPersistArgs{Enabled: true}); !res.Persistent || res.Changed || len(run.Calls()) != 0 {
		t.Fatalf("again = %+v %q", res, cmds(run))
	}
	if res, _ := o.journalPersist(ctx, JournalPersistArgs{Status: true}); !res.Persistent || res.Changed {
		t.Fatalf("status = %+v", res)
	}

	res, err = o.journalPersist(ctx, JournalPersistArgs{Enabled: false})
	if err != nil || res.Persistent || !res.Changed || files.Exists(JournaldDropIn) {
		t.Fatalf("off = %+v %v", res, err)
	}
	if got := cmds(run); !slices.Equal(got, []string{"systemctl restart systemd-journald.service"}) {
		t.Fatalf("commands = %q", got)
	}
}

// The administrator set Storage= in a file of their own: NasBuster leaves it
// alone, and withdraws its drop-in, which would override theirs.
func TestJournalPersistLeavesTheAdminsSetting(t *testing.T) {
	for name, admin := range map[string]string{
		"journald.conf":          journaldConf,
		"a drop-in of their own": "/etc/systemd/journald.conf.d/50-cwnsb-test-persistent.conf",
	} {
		t.Run(name, func(t *testing.T) {
			files := NewMemFiles()
			files.WriteFile(JournaldDropIn, []byte(journaldDropInContent), 0o644)
			files.WriteFile(admin, []byte("[Journal]\n  Storage=volatile\n"), 0o644)
			o, run := newJournaldOps(files)
			ctx := context.Background()

			if res, _ := o.journalPersist(ctx, JournalPersistArgs{Status: true}); res.ManagedBy != admin || res.Changed || !files.Exists(JournaldDropIn) {
				t.Fatalf("status = %+v", res)
			}
			res, err := o.journalPersist(ctx, JournalPersistArgs{Enabled: true})
			if err != nil || res.ManagedBy != admin || res.Persistent || !res.Changed || files.Exists(JournaldDropIn) {
				t.Fatalf("on = %+v %v", res, err)
			}
			if b, _ := files.ReadFile(admin); !strings.Contains(string(b), "Storage=volatile") {
				t.Fatalf("the admin's file was changed: %q", b)
			}
			if got := cmds(run); !slices.Equal(got, []string{"systemctl restart systemd-journald.service"}) {
				t.Fatalf("commands = %q", got)
			}
		})
	}
}
