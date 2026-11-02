// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"encoding/json"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/crosswax/nasbuster/internal/backup"
	"github.com/crosswax/nasbuster/internal/shares"
	"github.com/crosswax/nasbuster/internal/storage"
	"github.com/crosswax/nasbuster/internal/validate"
)

// Configuration backups on the pools (ADR-0014). The daemon leaves a
// consistent copy of its database at a fixed path; the helper reads it
// without following links (a compromised daemon must not make root copy
// /etc/shadow), adds what only root can read (Samba's password hashes, the
// UIDs and GIDs that own the files) and stores it on every mounted pool.

// mountedPools lists the CWNSB pools known to this system and mounted.
func (o *Ops) mountedPools(ctx context.Context) []string {
	units, _ := o.Files.ReadDir(o.UnitDir)
	var out []string
	for _, u := range units {
		name, ok := strings.CutSuffix(strings.TrimPrefix(u, "srv-cwnsb-"), ".mount")
		if !ok || !strings.HasPrefix(u, "srv-cwnsb-") || !storage.ValidPoolName(name) {
			continue
		}
		if m := o.mountInfo(ctx, storage.MountPoint(name)); m.Mounted && m.FSType == "btrfs" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// maxBackupShares bounds the share names a manifest lists.
const maxBackupShares = 256

// configBackup stores the configuration on every mounted pool, then rotates
// the backups this installation made there; never those of another one (P6).
func (o *Ops) configBackup(ctx context.Context, args BackupArgs) (BackupResult, *Error) {
	if args.Installation != "" && !backup.ValidInstallation(args.Installation) {
		return BackupResult{}, errorf(CodeInvalidArgs, "invalid installation id")
	}
	if args.Admin != "" && !validate.Username(args.Admin) {
		return BackupResult{}, errorf(CodeInvalidArgs, "invalid admin name")
	}
	if len(args.Shares) > maxBackupShares {
		return BackupResult{}, errorf(CodeInvalidArgs, "too many shares")
	}
	shareNames := []string{}
	for _, s := range args.Shares {
		if !shares.ValidShareName(s) {
			return BackupResult{}, errorf(CodeInvalidArgs, "invalid share name")
		}
		shareNames = append(shareNames, s)
	}
	read := o.ReadDaemonExport
	if read == nil {
		read = readDaemonExport
	}
	db, err := read()
	if err != nil {
		return BackupResult{}, errorf(CodeFailed, "read the daemon's export: %v", err)
	}
	if !backup.IsSQLite(db) || len(db) > backup.MaxDatabase {
		return BackupResult{}, errorf(CodeFailed, "the daemon's export is not an SQLite database")
	}
	passwd, _ := fs.ReadFile(o.FS, "etc/passwd")
	group, _ := fs.ReadFile(o.FS, "etc/group")
	users, groups := backup.Identities(passwd, group)
	var samba []byte
	if res, err := o.Run.Run(ctx, "pdbedit", "-L", "-w"); err == nil && res.ExitCode == 0 {
		samba = backup.FilterSamba(res.Stdout, users)
	}
	hostname, _ := fs.ReadFile(o.FS, "proc/sys/kernel/hostname")
	now := time.Now()
	if o.Now != nil {
		now = o.Now()
	}
	manifest, _ := json.MarshalIndent(backup.Manifest{Version: backup.ManifestVersion, Created: now.UTC(), Hostname: strings.TrimSpace(string(hostname)),
		Users: users, Groups: groups, Installation: args.Installation, Admin: args.Admin, Shares: shareNames}, "", "  ")

	name := backup.Name(now)
	out := BackupResult{Name: name, Pools: []string{}}
	for _, pool := range o.mountedPools(ctx) {
		base := backup.Dir(pool)
		dir := path.Join(base, name)
		if err := o.Files.MkdirAll(dir, 0o700); err != nil {
			return out, errorf(CodeFailed, "create %s: %v", dir, err)
		}
		for file, data := range map[string][]byte{backup.DatabaseFile: db, backup.SambaFile: samba, backup.ManifestFile: manifest} {
			if err := o.Files.WriteFile(path.Join(dir, file), data, 0o600); err != nil {
				return out, errorf(CodeFailed, "write %s: %v", path.Join(dir, file), err)
			}
		}
		for _, old := range backup.PruneFor(o.backupEntries(base), args.Installation) {
			for _, f := range []string{backup.DatabaseFile, backup.SambaFile, backup.ManifestFile} {
				_ = o.Files.Remove(path.Join(base, old, f))
			}
			_ = o.Files.Remove(path.Join(base, old))
		}
		out.Pools = append(out.Pools, pool)
	}
	// Every number in the backup keeps its place in the pools' ledger (P3),
	// accounts made before the ledger existed included.
	var uids, gids []int
	for _, u := range users {
		uids, gids = append(uids, u.UID), append(gids, u.GID)
	}
	for _, g := range groups {
		gids = append(gids, g.GID)
	}
	o.recordIDs(ctx, uids, gids)
	return out, nil
}

// backupEntries lists the backups of a pool with the installation that made
// each one. An unreadable manifest gives no installation: never rotated.
func (o *Ops) backupEntries(base string) []backup.Entry {
	names, _ := o.Files.ReadDir(base)
	var out []backup.Entry
	for _, n := range names {
		if !backup.ValidName(n) {
			continue
		}
		e := backup.Entry{Name: n}
		if m, ok := o.readManifest(path.Join(base, n)); ok {
			e.Installation = m.Installation
		}
		out = append(out, e)
	}
	return out
}

func (o *Ops) readManifest(dir string) (backup.Manifest, bool) {
	var m backup.Manifest
	b, err := o.Files.ReadFile(path.Join(dir, backup.ManifestFile))
	if err != nil || json.Unmarshal(b, &m) != nil {
		return backup.Manifest{}, false
	}
	return m, true
}

// configBackups lists the backups found on the mounted pools.
func (o *Ops) configBackups(ctx context.Context) (BackupList, *Error) {
	out := BackupList{Backups: []BackupInfo{}}
	for _, pool := range o.mountedPools(ctx) {
		base := backup.Dir(pool)
		names, err := o.Files.ReadDir(base)
		if err != nil {
			continue
		}
		for _, n := range names {
			if !backup.ValidName(n) {
				continue
			}
			info := BackupInfo{Pool: pool, Name: n, Shares: -1}
			if t, ok := backup.Time(n); ok {
				info.Time = t.Unix()
			}
			if m, ok := o.readManifest(path.Join(base, n)); ok {
				info.Hostname, info.Users = m.Hostname, len(m.Users)
				info.Installation, info.Admin = m.Installation, m.Admin
				if m.Shares != nil {
					info.Shares = len(m.Shares)
				}
			}
			if b, err := o.Files.ReadFile(path.Join(base, n, backup.DatabaseFile)); err == nil {
				info.SizeBytes = int64(len(b))
			}
			out.Backups = append(out.Backups, info)
		}
	}
	sort.Slice(out.Backups, func(i, j int) bool {
		if out.Backups[i].Time != out.Backups[j].Time {
			return out.Backups[i].Time > out.Backups[j].Time
		}
		return out.Backups[i].Pool < out.Backups[j].Pool
	})
	return out, nil
}
