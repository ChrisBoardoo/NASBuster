// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/crosswax/nasbuster/internal/backup"
	"github.com/crosswax/nasbuster/internal/storage"
)

// Identity numbers on the pools (P3). A UID or GID that once owned files on a
// pool must never go to a new account or group: after an import, or on a new
// SD card, the newcomer would own someone else's files. Phase F: a share group
// created by the onboarding got the GID of the old share "lanparty", and its
// members could read and change it on disk.
//
// The pools say which numbers they know: the manifests of their configuration
// backups (every installation's, never rotated away by another one since
// ADR-0038) and a ledger, <pool>/.cwnsb/ids.json, of every number the helper
// gave or restored while the pool was mounted. New numbers come from a single
// counter above all of these and above what the system uses, as useradd does,
// but a number is never given twice.

const (
	idsFile = "ids.json"
	// firstNASID: 1000 belongs to the machine's first account (Raspberry Pi
	// Imager, the Debian installer), and container images often write as 1000.
	firstNASID = 1001
	lastNASID  = 59999 // Debian's UID_MAX and GID_MAX are 60000
)

func idsPath(pool string) string { return storage.MountPoint(pool) + "/.cwnsb/" + idsFile }

// idLedger is what a pool remembers of the numbers it has seen.
type idLedger struct {
	UIDs []int `json:"uids"`
	GIDs []int `json:"gids"`
}

// knownIDs gathers the numbers known to the mounted pools. A ledger that
// exists but cannot be read is an error: without it, a number could be given
// twice (a check that cannot answer says no).
func (o *Ops) knownIDs(ctx context.Context) (uids, gids map[int]bool, opErr *Error) {
	uids, gids = map[int]bool{}, map[int]bool{}
	for _, pool := range o.mountedPools(ctx) {
		if b, err := o.Files.ReadFile(idsPath(pool)); err == nil {
			var l idLedger
			if json.Unmarshal(b, &l) != nil {
				return nil, nil, errorf(CodeFailed, "the identity ledger of pool %s is unreadable", pool)
			}
			for _, id := range l.UIDs {
				uids[id] = true
			}
			for _, id := range l.GIDs {
				gids[id] = true
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, nil, errorf(CodeFailed, "read the identity ledger of pool %s: %v", pool, err)
		}
		base := backup.Dir(pool)
		names, _ := o.Files.ReadDir(base)
		for _, n := range names {
			if !backup.ValidName(n) {
				continue
			}
			m, ok := o.readManifest(path.Join(base, n))
			if !ok {
				continue
			}
			for _, u := range m.Users {
				uids[u.UID], gids[u.GID] = true, true
			}
			for _, g := range m.Groups {
				gids[g.GID] = true
			}
		}
	}
	return uids, gids, nil
}

// recordIDs adds numbers to the ledger of every mounted pool. A pool that
// cannot take them is logged: creating an account must not fail for it, and
// the backups of the next night list the numbers again.
func (o *Ops) recordIDs(ctx context.Context, uids, gids []int) {
	if len(uids) == 0 && len(gids) == 0 {
		return
	}
	for _, pool := range o.mountedPools(ctx) {
		var l idLedger
		if b, err := o.Files.ReadFile(idsPath(pool)); err == nil && json.Unmarshal(b, &l) != nil {
			o.Logger.Warn("identity ledger unreadable, not rewritten", "pool", pool)
			continue
		}
		before := len(l.UIDs) + len(l.GIDs)
		l.UIDs = mergeIDs(l.UIDs, uids)
		l.GIDs = mergeIDs(l.GIDs, gids)
		if len(l.UIDs)+len(l.GIDs) == before {
			continue
		}
		data, _ := json.Marshal(l)
		dir := path.Dir(idsPath(pool))
		if err := o.Files.MkdirAll(dir, 0o700); err != nil {
			o.Logger.Warn("identity ledger not written", "pool", pool, "err", err)
			continue
		}
		if err := o.Files.WriteFile(idsPath(pool), data, 0o600); err != nil {
			o.Logger.Warn("identity ledger not written", "pool", pool, "err", err)
		}
	}
}

func mergeIDs(have, add []int) []int {
	for _, id := range add {
		if !slices.Contains(have, id) {
			have = append(have, id)
		}
	}
	slices.Sort(have)
	return have
}

// systemIDs reads the UIDs, primary GIDs and group GIDs of /etc/passwd and
// /etc/group. A missing file counts as empty: useradd and groupadd refuse a
// number the system already uses anyway; the pools are what they cannot see.
func (o *Ops) systemIDs() (ids []int) {
	if o.FS == nil {
		return nil
	}
	for _, file := range []string{"etc/passwd", "etc/group"} {
		data, err := fs.ReadFile(o.FS, file)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			f := strings.Split(line, ":")
			if len(f) < 3 {
				continue
			}
			for _, field := range f[2:min(len(f), 4)] {
				if id, err := strconv.Atoi(field); err == nil {
					ids = append(ids, id)
				}
			}
		}
	}
	return ids
}

// nextNASID picks the number of a new NAS account (UID and the GID of its own
// group) or NAS group: one above every number in the NAS range that the
// system uses or the pools know, so that none is ever given twice.
func (o *Ops) nextNASID(ctx context.Context) (int, *Error) {
	uids, gids, err := o.knownIDs(ctx)
	if err != nil {
		return 0, err
	}
	top := firstNASID - 1
	consider := func(id int) {
		if id >= firstNASID && id <= lastNASID && id > top {
			top = id
		}
	}
	for _, id := range o.systemIDs() {
		consider(id)
	}
	for id := range uids {
		consider(id)
	}
	for id := range gids {
		consider(id)
	}
	if top >= lastNASID {
		return 0, errorf(CodeFailed, "no account number left below %d", lastNASID)
	}
	return top + 1, nil
}
