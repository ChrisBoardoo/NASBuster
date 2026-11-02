// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"sort"
	"time"

	"github.com/crosswax/nasbuster/internal/shares"
	"github.com/crosswax/nasbuster/internal/snapshots"
)

func (o *Ops) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// shareDir validates a (pool, share) pair and returns the share's directory.
func (o *Ops) shareDir(ctx context.Context, pool, share string) (string, *Error) {
	if !shares.ValidShareName(share) {
		return "", errorf(CodeShareName, "invalid share name")
	}
	if err := o.requireMountedPool(ctx, pool); err != nil {
		return "", err
	}
	dir := shares.SharePath(pool, share)
	if !o.Files.Exists(dir) {
		return "", errorf(CodeShareMissing, "share %s has no directory", share)
	}
	return dir, nil
}

// snapshotCreate takes a read-only snapshot of a share. The name comes from
// the helper's clock, never from the caller.
func (o *Ops) snapshotCreate(ctx context.Context, a SnapshotArgs) (map[string]string, *Error) {
	dir, err := o.shareDir(ctx, a.Pool, a.Share)
	if err != nil {
		return nil, err
	}
	snapDir := path.Join(dir, snapshots.Dir)
	if !o.Files.Exists(snapDir) {
		// Readable (not writable) by the share's users: previous versions.
		if err := o.Files.MkdirAll(snapDir, 0o750); err != nil {
			return nil, errorf(CodeFailed, "create %s: %v", snapDir, err)
		}
		if err := o.check(ctx, "chown", "root:"+shares.ShareGroup(a.Share), snapDir); err != nil {
			return nil, err
		}
		if err := o.check(ctx, "chmod", "0750", snapDir); err != nil {
			return nil, err
		}
	}
	name := snapshots.Name(o.now())
	target := path.Join(snapDir, name)
	if o.Files.Exists(target) {
		return nil, errorf(CodeSnapshotExist, "snapshot %s already exists", name)
	}
	if err := o.check(ctx, "btrfs", "subvolume", "snapshot", "-r", dir, target); err != nil {
		return nil, err
	}
	return map[string]string{"name": name}, nil
}

// snapshotList returns the share's snapshot names, newest first. Entries
// that are not well-formed snapshot names are ignored.
func (o *Ops) snapshotList(ctx context.Context, a SnapshotArgs) (SnapshotListResult, *Error) {
	out := SnapshotListResult{Snapshots: []string{}}
	dir, err := o.shareDir(ctx, a.Pool, a.Share)
	if err != nil {
		return out, err
	}
	names, rerr := o.Files.ReadDir(path.Join(dir, snapshots.Dir))
	if errors.Is(rerr, fs.ErrNotExist) {
		return out, nil
	}
	if rerr != nil {
		return out, errorf(CodeFailed, "list snapshots: %v", rerr)
	}
	for _, n := range names {
		if snapshots.ValidName(n) {
			out.Snapshots = append(out.Snapshots, n)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out.Snapshots))) // the format sorts chronologically
	return out, nil
}

// snapshotDelete removes one snapshot. Only "<share>/.snapshots/@GMT-…" can
// ever be targeted: the name must match the snapshot format exactly.
func (o *Ops) snapshotDelete(ctx context.Context, a SnapshotArgs) (map[string]string, *Error) {
	if !snapshots.ValidName(a.Name) {
		return nil, errorf(CodeSnapshotName, "invalid snapshot name")
	}
	dir, err := o.shareDir(ctx, a.Pool, a.Share)
	if err != nil {
		return nil, err
	}
	target := path.Join(dir, snapshots.Dir, a.Name)
	if !o.Files.Exists(target) {
		return nil, errorf(CodeSnapshotNone, "no snapshot %s", a.Name)
	}
	if err := o.check(ctx, "btrfs", "subvolume", "delete", target); err != nil {
		return nil, err
	}
	return map[string]string{"name": a.Name}, nil
}
