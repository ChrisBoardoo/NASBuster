// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"path"
	"strings"

	"github.com/crosswax/nasbuster/internal/storage"
)

// poolUnitsRefresh rewrites the mount units the helper wrote earlier with the
// current template (same pool, same filesystem UUID): a package upgrade brings
// existing pools the fixes of new ones. It takes effect at the next boot, and
// nothing is remounted. Units without our marker are never touched.
func (o *Ops) poolUnitsRefresh(ctx context.Context) (PoolUnitsResult, *Error) {
	out := PoolUnitsResult{Updated: []string{}}
	if !o.Files.Exists(o.UnitDir) {
		return out, nil
	}
	names, err := o.Files.ReadDir(o.UnitDir)
	if err != nil {
		return out, errorf(CodeFailed, "list units: %v", err)
	}
	for _, n := range names {
		pool, ok := strings.CutPrefix(n, "srv-cwnsb-")
		if !ok {
			continue
		}
		if pool, ok = strings.CutSuffix(pool, ".mount"); !ok || !storage.ValidPoolName(pool) {
			continue
		}
		p := path.Join(o.UnitDir, n)
		old, err := o.Files.ReadFile(p)
		if err != nil || !strings.HasPrefix(string(old), storage.UnitMarker) {
			continue
		}
		uuid, ok := o.unitUUID(pool)
		if !ok {
			continue
		}
		unit := storage.MountUnit(pool, uuid)
		if string(old) == unit {
			continue
		}
		if err := o.Files.WriteFile(p, []byte(unit), 0o644); err != nil {
			return out, errorf(CodeFailed, "write %s: %v", n, err)
		}
		out.Updated = append(out.Updated, pool)
	}
	if len(out.Updated) > 0 {
		if err := o.check(ctx, "systemctl", "daemon-reload"); err != nil {
			return out, err
		}
	}
	return out, nil
}
