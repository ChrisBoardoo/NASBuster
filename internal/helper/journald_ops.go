// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"path"
	"strings"
)

// The system journal kept across reboots (ADR-0031): on by default, off on
// request. Raspberry Pi OS keeps it in memory only, to spare the SD card
// (/usr/lib/systemd/journald.conf.d/40-rpi-volatile-storage.conf); a drop-in
// of NasBuster's own, later in name order, makes it persistent and capped.
// An administrator's own Storage= setting always wins.

// JournaldDropIn is NasBuster's drop-in; the package removes it when removed.
const JournaldDropIn = "/etc/systemd/journald.conf.d/60-cwnsb-persistent.conf"

const journaldConf = "/etc/systemd/journald.conf"

const journaldDropInContent = "# Managed by CrossWax NasBuster (ADR-0031): the system journal is kept across\n" +
	"# reboots, to explain a failure afterwards. Turn it off in NasBuster's settings.\n" +
	"[Journal]\nStorage=persistent\nSystemMaxUse=64M\n"

func (o *Ops) journalPersist(ctx context.Context, a JournalPersistArgs) (JournalPersistResult, *Error) {
	var out JournalPersistResult
	current, err := o.Files.ReadFile(JournaldDropIn)
	ours := err == nil && string(current) == journaldDropInContent
	if other := o.journaldStorageElsewhere(); other != "" {
		out.ManagedBy = other
		if ours && !a.Status {
			// Ours comes later in name order and would override the admin's.
			if err := o.Files.Remove(JournaldDropIn); err != nil {
				return out, errorf(CodeFailed, "remove %s: %v", JournaldDropIn, err)
			}
			out.Changed = true
			o.restartJournald(ctx)
		}
		return out, nil
	}
	out.Persistent = ours
	if a.Status || a.Enabled == ours {
		return out, nil
	}
	if a.Enabled {
		if err := o.Files.MkdirAll(path.Dir(JournaldDropIn), 0o755); err != nil {
			return out, errorf(CodeFailed, "create %s: %v", path.Dir(JournaldDropIn), err)
		}
		if err := o.Files.WriteFile(JournaldDropIn, []byte(journaldDropInContent), 0o644); err != nil {
			return out, errorf(CodeFailed, "write %s: %v", JournaldDropIn, err)
		}
		// /var/log/journal with the owner, group and ACLs systemd expects.
		o.try(ctx, "systemd-tmpfiles", "--create", "--prefix", "/var/log/journal")
	} else if err := o.Files.Remove(JournaldDropIn); err != nil {
		return out, errorf(CodeFailed, "remove %s: %v", JournaldDropIn, err)
	}
	out.Persistent, out.Changed = a.Enabled, true
	o.restartJournald(ctx)
	if a.Enabled {
		o.try(ctx, "journalctl", "--flush") // what this boot logged so far goes to disk too
	}
	return out, nil
}

// restartJournald applies a Storage= change; on failure, the next boot does.
func (o *Ops) restartJournald(ctx context.Context) {
	if err := o.check(ctx, "systemctl", "restart", "systemd-journald.service"); err != nil {
		o.logger().Warn("journald not restarted: the change applies at the next boot", "err", err.Message)
	}
}

// journaldStorageElsewhere names the administrator's file that sets Storage=
// (journald.conf or another drop-in under /etc), "" if none does.
func (o *Ops) journaldStorageElsewhere() string {
	files := []string{journaldConf}
	dir := path.Dir(JournaldDropIn)
	if names, err := o.Files.ReadDir(dir); err == nil {
		for _, n := range names {
			if strings.HasSuffix(n, ".conf") && path.Join(dir, n) != JournaldDropIn {
				files = append(files, path.Join(dir, n))
			}
		}
	}
	for _, f := range files {
		b, err := o.Files.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "Storage=") {
				return f
			}
		}
	}
	return ""
}
