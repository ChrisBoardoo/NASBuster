// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/crosswax/nasbuster/internal/backup"
	"github.com/crosswax/nasbuster/internal/shares"
	"github.com/crosswax/nasbuster/internal/storage"
)

// RestoreTarget is where the helper leaves the database to restore; the
// daemon swaps it in when it starts.
const RestoreTarget = "/var/lib/cwnsb/restore/config.db"

// configRestore brings back the identities and Samba passwords of a backup
// and leaves its database for the daemon. Accounts are recreated with their
// old UIDs and GIDs, which own the files on the pools. An account whose name
// or ID is taken differently on this system is skipped and reported, never
// forced.
func (o *Ops) configRestore(ctx context.Context, a RestoreArgs) (RestoreConfigResult, *Error) {
	var out RestoreConfigResult
	if !storage.ValidPoolName(a.Pool) || !backup.ValidName(a.Name) {
		return out, errorf(CodeInvalidArgs, "invalid pool or backup name")
	}
	if m := o.mountInfo(ctx, storage.MountPoint(a.Pool)); !m.Mounted {
		return out, errorf(CodePoolNotMounted, "pool %s is not mounted", a.Pool)
	}
	dir := path.Join(backup.Dir(a.Pool), a.Name)
	db, err := o.Files.ReadFile(path.Join(dir, backup.DatabaseFile))
	if err != nil || !backup.IsSQLite(db) || len(db) > backup.MaxDatabase {
		return out, errorf(CodeBackupInvalid, "backup %s has no valid database", a.Name)
	}
	mb, err := o.Files.ReadFile(path.Join(dir, backup.ManifestFile))
	var m backup.Manifest
	if err != nil || json.Unmarshal(mb, &m) != nil || !backup.SupportedVersion(m.Version) {
		return out, errorf(CodeBackupInvalid, "backup %s has no valid manifest", a.Name)
	}
	out.Conflicts = []string{}
	out.Users = []string{}

	passwd, _ := fs.ReadFile(o.FS, "etc/passwd")
	group, _ := fs.ReadFile(o.FS, "etc/group")
	byName, byID := idMaps(passwd)
	gByName, gByID := idMaps(group)

	// Groups first: users' primary groups, cwnsb-users, share groups.
	okGroup := map[string]bool{}
	for _, g := range m.Groups {
		if !validIdentity(g.Name) || g.GID < 100 {
			out.Conflicts = append(out.Conflicts, "group "+g.Name+": invalid")
			continue
		}
		switch gid, exists := gByName[g.Name]; {
		case exists && gid == g.GID:
			okGroup[g.Name] = true
		case g.Name == shares.UsersGroup && exists:
			// cwnsb-users owns no file: it only tells Samba who a NAS user
			// is (valid users). A fresh system where a NAS user was created
			// before the restore holds it under another GID (Pi 5, phase F,
			// 983 instead of 981): it is taken as it is.
			okGroup[g.Name] = true
		case g.Name == shares.UsersGroup && gByID[g.GID] != "":
			if err := o.ensureGroup(ctx, g.Name); err != nil { // any free GID
				out.Conflicts = append(out.Conflicts, "group "+g.Name+": "+err.Message)
				continue
			}
			okGroup[g.Name] = true
		case exists:
			out.Conflicts = append(out.Conflicts, "group "+g.Name+": exists with GID "+strconv.Itoa(gid))
		case gByID[g.GID] != "":
			out.Conflicts = append(out.Conflicts, "group "+g.Name+": GID "+strconv.Itoa(g.GID)+" taken by "+gByID[g.GID])
		default:
			if err := o.check(ctx, "groupadd", "--gid", strconv.Itoa(g.GID), g.Name); err != nil {
				out.Conflicts = append(out.Conflicts, "group "+g.Name+": "+err.Message)
				continue
			}
			okGroup[g.Name] = true
		}
	}
	// Users, as the helper creates them (nologin, member of cwnsb-users).
	var restored []backup.User
	for _, u := range m.Users {
		if !shares.ValidUserName(u.Name) || u.UID < 1000 {
			out.Conflicts = append(out.Conflicts, "user "+u.Name+": invalid")
			continue
		}
		switch uid, exists := byName[u.Name]; {
		case exists && uid == u.UID:
			_, member, _ := o.nasUser(ctx, u.Name)
			if !member {
				out.Conflicts = append(out.Conflicts, "user "+u.Name+": exists and is not a NAS user")
				continue
			}
		case exists:
			out.Conflicts = append(out.Conflicts, "user "+u.Name+": exists with UID "+strconv.Itoa(uid))
			continue
		case byID[u.UID] != "":
			out.Conflicts = append(out.Conflicts, "user "+u.Name+": UID "+strconv.Itoa(u.UID)+" taken by "+byID[u.UID])
			continue
		default:
			if !okGroup[shares.UsersGroup] {
				out.Conflicts = append(out.Conflicts, "user "+u.Name+": group "+shares.UsersGroup+" missing")
				continue
			}
			if err := o.check(ctx, "useradd", "--no-create-home", "--home-dir", "/nonexistent", "--shell", "/usr/sbin/nologin",
				"--uid", strconv.Itoa(u.UID), "--gid", strconv.Itoa(u.GID), "--groups", shares.UsersGroup,
				"--comment", "CrossWax NasBuster user", u.Name); err != nil {
				out.Conflicts = append(out.Conflicts, "user "+u.Name+": "+err.Message)
				continue
			}
		}
		restored = append(restored, u)
		out.Users = append(out.Users, u.Name)
	}
	// The restored numbers own files: the pools keep them for good (P3).
	var ruids, rgids []int
	for _, u := range restored {
		ruids, rgids = append(ruids, u.UID), append(rgids, u.GID)
	}
	for _, g := range m.Groups {
		if okGroup[g.Name] {
			rgids = append(rgids, g.GID)
		}
	}
	o.recordIDs(ctx, ruids, rgids)
	// Samba passwords of the restored users only.
	if len(restored) > 0 {
		if samba, err := o.Files.ReadFile(path.Join(dir, backup.SambaFile)); err == nil {
			tmp := path.Join(dir, ".restore-smbpasswd")
			if err := o.Files.WriteFile(tmp, backup.FilterSamba(samba, restored), 0o600); err != nil {
				return out, errorf(CodeFailed, "prepare Samba import: %v", err)
			}
			err := o.check(ctx, "pdbedit", "--import=smbpasswd:"+tmp, "--export=tdbsam")
			_ = o.Files.Remove(tmp)
			if err != nil {
				return out, err
			}
		}
	}
	// The database, for the daemon.
	owner, ok := byName["cwnsb"]
	if !ok {
		return out, errorf(CodeFailed, "service account cwnsb missing")
	}
	if err := o.Files.MkdirAll(path.Dir(RestoreTarget), 0o750); err != nil {
		return out, errorf(CodeFailed, "create %s: %v", path.Dir(RestoreTarget), err)
	}
	if err := o.Files.WriteFile(RestoreTarget, db, 0o600); err != nil {
		return out, errorf(CodeFailed, "write %s: %v", RestoreTarget, err)
	}
	if o.Chown != nil {
		for _, p := range []string{path.Dir(RestoreTarget), RestoreTarget} {
			if err := o.Chown(p, owner, gByName["cwnsb"]); err != nil {
				return out, errorf(CodeFailed, "chown %s: %v", p, err)
			}
		}
	}
	out.Restart = true
	return out, nil
}

// idMaps reads /etc/passwd or /etc/group: name -> id and id -> name.
func idMaps(data []byte) (map[string]int, map[int]string) {
	byName, byID := map[string]int{}, map[int]string{}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Split(line, ":")
		if len(f) < 3 {
			continue
		}
		id, err := strconv.Atoi(f[2])
		if err != nil {
			continue
		}
		byName[f[0]] = id
		if _, dup := byID[id]; !dup {
			byID[id] = f[0]
		}
	}
	return byName, byID
}

// validIdentity accepts the group names a backup may hold.
func validIdentity(name string) bool {
	return name == shares.UsersGroup || shares.ValidUserName(name) ||
		(strings.HasPrefix(name, "cwnsb-share-") && shares.ValidShareName(strings.TrimPrefix(name, "cwnsb-share-")))
}

// osChown changes the owner of a file (the daemon must read the restored database).
func osChown(p string, uid, gid int) error { return os.Chown(p, uid, gid) }
