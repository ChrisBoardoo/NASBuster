// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"encoding/json"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/crosswax/nasbuster/internal/importer"
	"github.com/crosswax/nasbuster/internal/shares"
	"github.com/crosswax/nasbuster/internal/storage"
)

// Copying data from a foreign disk into a share (ADR-0015). The source is
// only ever mounted read-only, under /run, and never written to.

// lsblkNode is the part of `lsblk -J` this feature needs.
type lsblkNode struct {
	KName       string      `json:"kname"`
	Type        string      `json:"type"`
	FSType      string      `json:"fstype"`
	Label       string      `json:"label"`
	PartLabel   string      `json:"partlabel"`
	UUID        string      `json:"uuid"`
	Size        json.Number `json:"size"`
	Mountpoints []*string   `json:"mountpoints"`
	Children    []lsblkNode `json:"children"`
}

func (n lsblkNode) mounts() []string {
	var out []string
	for _, m := range n.Mountpoints {
		if m != nil && *m != "" {
			out = append(out, *m)
		}
	}
	return out
}

// importSources lists the filesystems that can be copied from.
func (o *Ops) importSources(ctx context.Context) (ImportSources, *Error) {
	out := ImportSources{Sources: []ImportSource{}}
	_ = o.check(ctx, "udevadm", "settle", "--timeout=10")
	disks, err := o.Discover(ctx)
	if err != nil {
		return out, errorf(CodeFailed, "discover disks: %v", err)
	}
	allowed := map[string]string{} // kernel name -> disk id
	for _, d := range disks {
		if d.Role != storage.RoleSystem && d.Role != storage.RoleProtected {
			allowed[path.Base(d.DevPath)] = d.ID
		}
	}
	res, rerr := o.Run.Run(ctx, "lsblk", "-J", "-b", "-o", "NAME,KNAME,TYPE,FSTYPE,LABEL,PARTLABEL,UUID,SIZE,MOUNTPOINTS") // NAME: without it, lsblk prints a flat list, not a tree
	if rerr != nil || res.ExitCode != 0 {
		return out, errorf(CodeFailed, "lsblk: %s", firstLine(res.Stderr))
	}
	var tree struct {
		BlockDevices []lsblkNode `json:"blockdevices"`
	}
	if err := json.Unmarshal(res.Stdout, &tree); err != nil {
		return out, errorf(CodeFailed, "lsblk: %v", err)
	}
	byUUID := map[string]*ImportSource{}
	probed := map[string]map[string]string{}
	var walk func(n lsblkNode, disk string)
	walk = func(n lsblkNode, disk string) {
		for _, c := range n.Children {
			walk(c, disk)
		}
		// lsblk's FSTYPE, LABEL and UUID come from the udev database, which
		// may lag behind a recent mkfs: probe the device itself.
		props, done := probed[n.KName]
		if !done {
			props = map[string]string{}
			if res, err := o.Run.Run(ctx, "blkid", "-p", "-o", "export", "/dev/"+n.KName); err == nil && res.ExitCode == 0 {
				props = storage.ParseMDExport(string(res.Stdout))
			}
			probed[n.KName] = props
		}
		n.FSType, n.Label, n.UUID = props["TYPE"], props["LABEL"], props["UUID"]
		if pl := props["PART_ENTRY_NAME"]; pl != "" {
			n.PartLabel = pl
		}
		_, ok := importer.Supported(n.FSType)
		if !ok || !importer.ValidUUID(n.UUID) || strings.HasPrefix(n.Label, "cwnsb-") || strings.HasPrefix(n.PartLabel, "cwnsb-") {
			return // not copyable, or a CWNSB pool (import it as a pool instead)
		}
		mounts := n.mounts()
		ours := len(mounts) == 1 && mounts[0] == importer.MountPoint(n.UUID)
		if len(mounts) > 0 && !ours {
			return // in use elsewhere
		}
		src := byUUID[n.UUID]
		if src == nil {
			size, _ := strconv.ParseUint(n.Size.String(), 10, 64)
			src = &ImportSource{UUID: n.UUID, FSType: n.FSType, Label: n.Label, Device: n.KName, SizeBytes: size, Disks: []string{}, Mounted: ours}
			if ours {
				src.MountPoint = mounts[0]
			}
			byUUID[n.UUID] = src
		}
		if !contains(src.Disks, disk) {
			src.Disks = append(src.Disks, disk)
		}
	}
	for _, d := range tree.BlockDevices {
		if id, ok := allowed[d.KName]; ok {
			walk(d, id)
		}
	}
	for _, s := range byUUID {
		out.Sources = append(out.Sources, *s)
	}
	sort.Slice(out.Sources, func(i, j int) bool {
		return out.Sources[i].Label+out.Sources[i].UUID < out.Sources[j].Label+out.Sources[j].UUID
	})
	return out, nil
}

func (o *Ops) importSource(ctx context.Context, uuid string) (ImportSource, *Error) {
	if !importer.ValidUUID(uuid) {
		return ImportSource{}, errorf(CodeInvalidArgs, "invalid filesystem UUID")
	}
	list, err := o.importSources(ctx)
	if err != nil {
		return ImportSource{}, err
	}
	for _, s := range list.Sources {
		if s.UUID == uuid {
			return s, nil
		}
	}
	return ImportSource{}, errorf(CodeDiskNotFound, "no importable filesystem %s", uuid)
}

// importMount mounts a source read-only and lists its top level.
func (o *Ops) importMount(ctx context.Context, a ImportMountArgs) (ImportMounted, *Error) {
	src, err := o.importSource(ctx, a.UUID)
	if err != nil {
		return ImportMounted{}, err
	}
	mnt := importer.MountPoint(src.UUID)
	if !src.Mounted {
		mountType, _ := importer.Supported(src.FSType)
		if err := o.Files.MkdirAll(mnt, 0o700); err != nil {
			return ImportMounted{}, errorf(CodeFailed, "create %s: %v", mnt, err)
		}
		if err := o.checkTimeout(ctx, time.Minute, "mount", "-t", mountType, "-o", importer.MountOptions(src.FSType), "/dev/"+src.Device, mnt); err != nil {
			_ = o.Files.Remove(mnt)
			return ImportMounted{}, err
		}
	}
	names, _ := o.Files.ReadDir(mnt)
	if len(names) > 200 {
		names = names[:200]
	}
	if names == nil {
		names = []string{}
	}
	return ImportMounted{MountPoint: mnt, Entries: names}, nil
}

// importCopy copies a mounted source (or one of its folders) into a new
// folder of a share, in the background.
func (o *Ops) importCopy(ctx context.Context, a ImportCopyArgs) (map[string]string, *Error) {
	if !shares.ValidShareName(a.Share) || !importer.ValidSubdir(a.Subdir) {
		return nil, errorf(CodeInvalidArgs, "invalid share or folder")
	}
	src, err := o.importSource(ctx, a.UUID)
	if err != nil {
		return nil, err
	}
	if !src.Mounted {
		return nil, errorf(CodeImportNotMounted, "mount the source first")
	}
	shareDir, err := o.shareDir(ctx, a.Pool, a.Share)
	if err != nil {
		return nil, err
	}
	from := path.Join(src.MountPoint, a.Subdir)
	if !o.Files.Exists(from) {
		return nil, errorf(CodeInvalidArgs, "no folder %s on the source", a.Subdir)
	}
	now := time.Now()
	if o.Now != nil {
		now = o.Now()
	}
	label := src.Label
	if a.Subdir != "" {
		label += " " + path.Base(a.Subdir)
	}
	destName := importer.DestName(label, now)
	dest := path.Join(shareDir, destName)
	for i := 2; o.Files.Exists(dest); i++ { // never write into an existing folder
		if i > 99 {
			return nil, errorf(CodeFailed, "too many copies named %s", destName)
		}
		destName = importer.DestName(label, now) + " (" + strconv.Itoa(i) + ")"
		dest = path.Join(shareDir, destName)
	}
	job := importer.Job(a.Share)
	if o.job(ctx, job, 0).State == "running" {
		return nil, errorf(CodeJobRunning, "a copy into %s is already running", a.Share)
	}
	// The total, for the progress (bounded: a huge tree only loses the percentage).
	total := int64(0)
	dctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if res, err := o.Run.Run(dctx, "rsync", "-rlt", "--dry-run", "--stats", from+"/", dest+"/"); err == nil && res.ExitCode == 0 {
		total, _ = importer.ParseDryRun(string(res.Stdout))
	}
	_ = o.Files.Remove(importer.LogPath(a.Share))
	desc := "src=" + src.UUID + " total=" + strconv.FormatInt(total, 10) + " dest=" + destName
	if err := o.startJobDesc(ctx, job, desc, []string{"/usr/bin/rsync", "-rltp", "--chmod=D2770,F0660",
		"--log-file=" + importer.LogPath(a.Share), "--log-file-format=%l %n", from + "/", dest + "/"}); err != nil {
		return nil, err
	}
	return map[string]string{"dest": destName}, nil
}

// importProgress reports a copy: job state, total, copied so far.
func (o *Ops) importProgress(ctx context.Context, a ImportShareArgs) (ImportProgress, *Error) {
	if !shares.ValidShareName(a.Share) {
		return ImportProgress{}, errorf(CodeInvalidArgs, "invalid share")
	}
	job := importer.Job(a.Share)
	out := ImportProgress{Job: o.job(ctx, job, 5)}
	if res, err := o.Run.Run(ctx, "systemctl", "show", "cwnsb-job-"+job+".service", "-p", "Description", "--value"); err == nil {
		desc := strings.TrimSpace(string(res.Stdout))
		for _, f := range strings.SplitN(desc, " ", 3) {
			if k, v, ok := strings.Cut(f, "="); ok {
				switch k {
				case "src":
					out.Source = v
				case "total":
					out.TotalBytes, _ = strconv.ParseInt(v, 10, 64)
				case "dest":
					out.Dest = v
				}
			}
		}
		if i := strings.Index(desc, " dest="); i >= 0 {
			out.Dest = desc[i+len(" dest="):]
		}
	}
	if b, err := o.Files.ReadFile(importer.LogPath(a.Share)); err == nil {
		out.CopiedBytes, out.Files = importer.ParseCopyLog(string(b))
	}
	return out, nil
}

// importUnmount unmounts a source, unless a copy is reading from it.
func (o *Ops) importUnmount(ctx context.Context, a ImportMountArgs) (map[string]bool, *Error) {
	if !importer.ValidUUID(a.UUID) {
		return nil, errorf(CodeInvalidArgs, "invalid filesystem UUID")
	}
	mnt := importer.MountPoint(a.UUID)
	if res, err := o.Run.Run(ctx, "systemctl", "list-units", "cwnsb-job-import-*", "--all", "--plain", "--no-legend"); err == nil {
		for _, line := range strings.Split(string(res.Stdout), "\n") {
			f := strings.Fields(line)
			if len(f) < 3 || f[2] != "activating" {
				continue
			}
			if d, err := o.Run.Run(ctx, "systemctl", "show", f[0], "-p", "Description", "--value"); err == nil &&
				strings.HasPrefix(strings.TrimSpace(string(d.Stdout)), "src="+a.UUID+" ") {
				return nil, errorf(CodeJobRunning, "a copy is still reading from this disk")
			}
		}
	}
	if o.mountInfo(ctx, mnt).Mounted {
		if err := o.checkTimeout(ctx, time.Minute, "umount", mnt); err != nil {
			return nil, err
		}
	}
	_ = o.Files.Remove(mnt)
	return map[string]bool{"unmounted": true}, nil
}
