// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/crosswax/nasbuster/internal/shares"
	"github.com/crosswax/nasbuster/internal/snapshots"
	"github.com/crosswax/nasbuster/internal/storage"
)

// nasUser reports whether name exists on the system and whether it is a NAS
// user (member of cwnsb-users). The helper only ever manages NAS users.
func (o *Ops) nasUser(ctx context.Context, name string) (exists, member bool, opErr *Error) {
	res, err := o.Run.Run(ctx, "id", "-nG", name)
	if err != nil {
		return false, false, errorf(CodeFailed, "id: %v", err)
	}
	if res.ExitCode != 0 {
		return false, false, nil
	}
	return true, slices.Contains(strings.Fields(string(res.Stdout)), shares.UsersGroup), nil
}

// ensureGroup creates a NAS group (a share's, cwnsb-users) if it is missing,
// with a number no pool has ever seen (P3).
func (o *Ops) ensureGroup(ctx context.Context, group string) *Error {
	if res, err := o.Run.Run(ctx, "getent", "group", group); err == nil && res.ExitCode == 0 {
		return nil
	}
	o.idsMu.Lock()
	defer o.idsMu.Unlock()
	gid, err := o.nextNASID(ctx)
	if err != nil {
		return err
	}
	if err := o.check(ctx, "groupadd", "--gid", strconv.Itoa(gid), group); err != nil {
		return err
	}
	o.recordIDs(ctx, nil, []int{gid})
	return nil
}

func (o *Ops) requireNASUser(ctx context.Context, name string) *Error {
	if !shares.ValidUserName(name) {
		return errorf(CodeUserName, "invalid user name")
	}
	_, member, err := o.nasUser(ctx, name)
	if err != nil {
		return err
	}
	if !member {
		return errorf(CodeUserNotFound, "no NAS user named %s", name)
	}
	return nil
}

// setSMBPassword feeds the password twice on stdin (never as an argument).
func (o *Ops) setSMBPassword(ctx context.Context, name, password string, add bool) *Error {
	args := []string{"-s"}
	if add {
		args = append(args, "-a")
	}
	res, err := o.Run.RunInput(ctx, []byte(password+"\n"+password+"\n"), "smbpasswd", append(args, name)...)
	if err != nil {
		return errorf(CodeFailed, "smbpasswd: %v", err)
	}
	if res.ExitCode != 0 {
		return errorf(CodeFailed, "smbpasswd exited with %d: %s", res.ExitCode, firstLine(res.Stderr))
	}
	return nil
}

func (o *Ops) userCreate(ctx context.Context, a UserArgs) (map[string]string, *Error) {
	if !shares.ValidUserName(a.Name) {
		return nil, errorf(CodeUserName, "invalid user name")
	}
	if !shares.ValidPassword(a.Password) {
		return nil, errorf(CodeUserPassword, "password must be %d to %d characters, on one line", shares.PasswordMin, shares.PasswordMax)
	}
	exists, member, err := o.nasUser(ctx, a.Name)
	if err != nil {
		return nil, err
	}
	switch {
	case member:
		return nil, errorf(CodeUserExists, "user %s already exists", a.Name)
	case exists:
		// A system or personal account: never adopt or modify it.
		return nil, errorf(CodeUserNameTaken, "the name %s is used by another account on this system", a.Name)
	}
	if err := o.ensureGroup(ctx, shares.UsersGroup); err != nil {
		return nil, err
	}
	// The account and its own group take one number no pool has ever seen
	// (P3), instead of the first free one, which may own files on a pool.
	o.idsMu.Lock()
	defer o.idsMu.Unlock()
	id, err := o.nextNASID(ctx)
	if err != nil {
		return nil, err
	}
	num := strconv.Itoa(id)
	if err := o.check(ctx, "groupadd", "--gid", num, a.Name); err != nil {
		return nil, err
	}
	if err := o.check(ctx, "useradd", "--no-create-home", "--home-dir", "/nonexistent", "--shell", "/usr/sbin/nologin",
		"--uid", num, "--gid", num, "--groups", shares.UsersGroup, "--comment", "CrossWax NasBuster user", a.Name); err != nil {
		o.try(ctx, "groupdel", a.Name)
		return nil, err
	}
	// Recorded at once: even a failure below has taken the number.
	o.recordIDs(ctx, []int{id}, []int{id})
	if err := o.setSMBPassword(ctx, a.Name, a.Password, true); err != nil {
		o.try(ctx, "userdel", a.Name)
		o.try(ctx, "groupdel", a.Name)
		return nil, err
	}
	return map[string]string{"name": a.Name}, nil
}

func (o *Ops) userSetPassword(ctx context.Context, a UserArgs) (map[string]string, *Error) {
	if err := o.requireNASUser(ctx, a.Name); err != nil {
		return nil, err
	}
	if !shares.ValidPassword(a.Password) {
		return nil, errorf(CodeUserPassword, "password must be %d to %d characters, on one line", shares.PasswordMin, shares.PasswordMax)
	}
	if err := o.setSMBPassword(ctx, a.Name, a.Password, false); err != nil {
		return nil, err
	}
	return map[string]string{"name": a.Name}, nil
}

func (o *Ops) userDelete(ctx context.Context, a UserArgs) (map[string]string, *Error) {
	if err := o.requireNASUser(ctx, a.Name); err != nil {
		return nil, err
	}
	o.try(ctx, "smbpasswd", "-x", a.Name) // absent from passdb is fine
	if err := o.check(ctx, "userdel", a.Name); err != nil {
		return nil, err
	}
	return map[string]string{"name": a.Name}, nil
}

// requireMountedPool checks the pool is a mounted CWNSB Btrfs pool.
func (o *Ops) requireMountedPool(ctx context.Context, pool string) *Error {
	if !storage.ValidPoolName(pool) {
		return errorf(CodePoolName, "invalid pool name")
	}
	if mounted, fsType, _ := o.findmnt(ctx, storage.MountPoint(pool)); !mounted || fsType != "btrfs" {
		return errorf(CodePoolNotMounted, "pool %s is not mounted", pool)
	}
	return nil
}

func (o *Ops) shareCreate(ctx context.Context, a ShareCreateArgs) (map[string]string, *Error) {
	if !shares.ValidShareName(a.Name) {
		return nil, errorf(CodeShareName, "invalid share name")
	}
	if err := o.requireMountedPool(ctx, a.Pool); err != nil {
		return nil, err
	}
	path := shares.SharePath(a.Pool, a.Name)
	if o.Files.Exists(path) {
		// A previous attempt may have stopped after creating the subvolume
		// (Samba refused the new config). Adopt it only if it is an empty
		// subvolume: existing files must never be exposed to new users.
		entries, err := o.Files.ReadDir(path)
		if err != nil || len(entries) > 0 || o.check(ctx, "btrfs", "subvolume", "show", path) != nil {
			return nil, errorf(CodeShareExists, "%s already exists", path)
		}
	} else if err := o.check(ctx, "btrfs", "subvolume", "create", path); err != nil {
		return nil, err
	}
	group := shares.ShareGroup(a.Name)
	for _, step := range [][]string{
		nil, // ensureGroup
		{"chown", "root:" + group, path},
		{"chmod", "2770", path},
	} {
		var err *Error
		if step == nil {
			err = o.ensureGroup(ctx, group)
		} else {
			err = o.check(ctx, step[0], step[1:]...)
		}
		if err != nil {
			o.try(ctx, "btrfs", "subvolume", "delete", path) // empty: it was just created
			return nil, err
		}
	}
	return map[string]string{"path": path, "group": group}, nil
}

// smbApply renders smb.conf from the structured desired state, validates it
// with testparm, swaps it in atomically (keeping the previous version) and
// reloads Samba. No configuration text comes from the caller.
func (o *Ops) smbApply(ctx context.Context, a SMBApplyArgs) (map[string]int, *Error) {
	for _, s := range a.Shares {
		if err := s.Validate(); err != nil {
			return nil, errorf(CodeInvalidArgs, "%v", err)
		}
		if err := o.requireMountedPool(ctx, s.Pool); err != nil {
			return nil, err
		}
		if !o.Files.Exists(shares.SharePath(s.Pool, s.Name)) {
			return nil, errorf(CodeShareMissing, "share %s has no directory", s.Name)
		}
		for _, acc := range s.Access {
			if err := o.requireNASUser(ctx, acc.User); err != nil {
				return nil, err
			}
		}
	}
	conf, err := shares.Render(a.Shares)
	if err != nil {
		return nil, errorf(CodeInvalidArgs, "%v", err)
	}

	tmp := o.SMBConf + ".cwnsb-new"
	if err := o.Files.WriteFile(tmp, []byte(conf), 0o644); err != nil {
		return nil, errorf(CodeFailed, "write %s: %v", tmp, err)
	}
	defer o.Files.Remove(tmp)
	res, runErr := o.Run.Run(ctx, "testparm", "-s", tmp)
	if runErr != nil || res.ExitCode != 0 {
		return nil, errorf(CodeFailed, "testparm rejected the configuration: %s", firstLine(res.Stderr))
	}

	current, readErr := o.Files.ReadFile(o.SMBConf)
	if readErr == nil {
		orig := o.SMBConf + ".cwnsb-orig"
		if !strings.HasPrefix(string(current), shares.Header) && !o.Files.Exists(orig) {
			if err := o.Files.WriteFile(orig, current, 0o644); err != nil {
				return nil, errorf(CodeFailed, "back up the original smb.conf: %v", err)
			}
		}
		_ = o.Files.WriteFile(o.SMBConf+".cwnsb-prev", current, 0o644)
	}
	if err := o.Files.WriteFile(o.SMBConf, []byte(conf), 0o644); err != nil {
		return nil, errorf(CodeFailed, "write smb.conf: %v", err)
	}
	// Our smb.conf disables NetBIOS, but the samba package started nmbd before
	// we took the file over. Debian's nmbd.service only starts when NetBIOS is
	// enabled (ExecCondition): try-restart lets it stop (about 20 MB on the Pi 5).
	// Best effort: nmbd may be absent or already stopped.
	if readErr != nil || !strings.HasPrefix(string(current), shares.Header) {
		_, _ = o.Run.Run(ctx, "systemctl", "try-restart", "nmbd")
	}
	// Samba publishes its mDNS records (Time Machine: _adisk._tcp) when it
	// starts, not on reload (verified in the lab). Restart only when the Time
	// Machine shares change: a restart briefly drops connected clients.
	action := "reload-or-restart"
	if !slices.Equal(shares.TimeMachineShares(string(current)), shares.TimeMachineShares(conf)) {
		action = "restart"
	}
	if err := o.check(ctx, "systemctl", action, "smbd"); err != nil {
		return nil, err
	}
	return map[string]int{"shares": len(a.Shares)}, nil
}

func (o *Ops) smbSessions(ctx context.Context) (SMBSessionsResult, *Error) {
	out := SMBSessionsResult{Sessions: []shares.Session{}, Connections: []shares.Connection{}}
	res, err := o.Run.Run(ctx, "smbstatus", "--json")
	if err != nil || res.ExitCode != 0 {
		return out, nil // Samba not running or not installed: nobody is connected
	}
	sessions, conns, perr := shares.ParseSmbstatus(res.Stdout)
	if perr != nil {
		return out, errorf(CodeFailed, "%v", perr)
	}
	out.Sessions, out.Connections = sessions, conns
	return out, nil
}

// shareDelete removes a share's data for good: its snapshots, then its
// subvolume, then its Linux group. The caller has already removed the share
// from Samba, so nobody can open it any more. Only Btrfs subvolumes under
// /srv/cwnsb/<pool>/ are ever deleted, never with rm.
func (o *Ops) shareDelete(ctx context.Context, a ShareCreateArgs) (map[string]bool, *Error) {
	dir, err := o.shareDir(ctx, a.Pool, a.Name)
	if err != nil && err.Code != CodeShareMissing {
		return nil, err
	}
	if err == nil {
		if err := o.check(ctx, "btrfs", "subvolume", "show", dir); err != nil {
			return nil, errorf(CodeFailed, "%s is not a subvolume: refusing to delete it", dir)
		}
		snapDir := path.Join(dir, snapshots.Dir)
		names, rerr := o.Files.ReadDir(snapDir)
		if rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			return nil, errorf(CodeFailed, "list snapshots: %v", rerr)
		}
		for _, n := range names {
			if !snapshots.ValidName(n) {
				return nil, errorf(CodeFailed, "unexpected entry %s in %s: refusing to delete the share", n, snapDir)
			}
			if err := o.check(ctx, "btrfs", "subvolume", "delete", path.Join(snapDir, n)); err != nil {
				return nil, err
			}
		}
		if err := o.check(ctx, "btrfs", "subvolume", "delete", dir); err != nil {
			return nil, err
		}
	}
	if o.groupExists(ctx, shares.ShareGroup(a.Name)) {
		if err := o.check(ctx, "groupdel", shares.ShareGroup(a.Name)); err != nil {
			return nil, err
		}
	}
	return map[string]bool{"deleted": true}, nil
}

func (o *Ops) groupExists(ctx context.Context, group string) bool {
	res, err := o.Run.Run(ctx, "getent", "group", group)
	return err == nil && res.ExitCode == 0
}

// recyclePurge deletes, in a share's recycle bin, the files deleted more than
// Days days ago (recycle:touch_mtime makes the deletion date the file's
// date), then the folders left empty. find never follows links nor leaves
// the filesystem.
func (o *Ops) recyclePurge(ctx context.Context, a RecyclePurgeArgs) (map[string]bool, *Error) {
	if a.Days < 1 || a.Days > 365 {
		return nil, errorf(CodeInvalidArgs, "days must be between 1 and 365")
	}
	dir, err := o.shareDir(ctx, a.Pool, a.Share)
	if err != nil {
		return nil, err
	}
	bin := path.Join(dir, shares.RecycleDir)
	if !o.Files.Exists(bin) {
		return map[string]bool{"purged": false}, nil
	}
	if err := o.checkTimeout(ctx, 10*time.Minute, "find", bin, "-xdev", "-mindepth", "1", "-type", "f",
		"-mtime", "+"+strconv.Itoa(a.Days-1), "-delete"); err != nil {
		return nil, err
	}
	o.try(ctx, "find", bin, "-xdev", "-mindepth", "1", "-type", "d", "-empty", "-delete")
	return map[string]bool{"purged": true}, nil
}
