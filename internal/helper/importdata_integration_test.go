// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build integration && linux

// Real-system test of copying data from a foreign disk into a share
// (ADR-0015), in the cwnsb-lab distro only.
package helper

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crosswax/nasbuster/internal/importer"
	"github.com/crosswax/nasbuster/internal/shares"
	"github.com/crosswax/nasbuster/internal/storage"
)

func TestLabImportFromOMVDisk(t *testing.T) {
	ctx := context.Background()
	l := newLabPool(t, 4)
	pool, _, err := l.create(storage.LayoutMirror, 0, 1)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	share := "itimport"
	if _, err := l.ops.shareCreate(ctx, ShareCreateArgs{Pool: pool, Name: share}); err != nil {
		t.Fatalf("share: %v", err)
	}
	t.Cleanup(func() {
		l.run.Run(ctx, "groupdel", shares.ShareGroup(share))
		l.run.Run(ctx, "systemctl", "stop", "cwnsb-job-"+importer.Job(share)+".service")
	})

	// An OMV disk: ext4 on a whole-disk md RAID1 (no partitions).
	md := "/dev/md/itomv"
	l.cmd("mdadm", "--create", md, "--run", "--level=1", "--raid-devices=2", "--metadata=1.2", "--name=0", "--homehost=openmediavault", l.loops[2], l.loops[3])
	t.Cleanup(func() {
		l.run.Run(ctx, "umount", "/mnt/itomv")
		l.run.Run(ctx, "mdadm", "--stop", md)
		l.run.Run(ctx, "mdadm", "--zero-superblock", l.loops[2])
		l.run.Run(ctx, "mdadm", "--zero-superblock", l.loops[3])
	})
	l.cmd("udevadm", "settle")
	l.cmd("mkfs.ext4", "-q", "-L", "OMVDATA", md)
	os.MkdirAll("/mnt/itomv", 0o755)
	l.cmd("mount", md, "/mnt/itomv")
	os.MkdirAll("/mnt/itomv/Photos/2024", 0o755)
	payload := make([]byte, 5<<20)
	for i := range payload {
		payload[i] = byte(i * 7)
	}
	os.WriteFile("/mnt/itomv/Photos/2024/beach.jpg", payload, 0o600)
	os.WriteFile("/mnt/itomv/readme.txt", []byte("from OMV\n"), 0o644)
	sum := sha256.Sum256(payload)
	l.cmd("umount", "/mnt/itomv")
	l.cmd("udevadm", "settle")

	list, ierr := l.ops.importSources(ctx)
	if ierr != nil {
		t.Fatalf("sources: %v", ierr)
	}
	var src ImportSource
	for _, s := range list.Sources {
		if s.Label == "OMVDATA" {
			src = s
		}
	}
	if src.UUID == "" || src.FSType != "ext4" || len(src.Disks) != 2 || src.Mounted {
		t.Fatalf("OMV source = %+v (all %+v)", src, list.Sources)
	}
	for _, s := range list.Sources {
		if strings.HasPrefix(s.Label, "cwnsb-") {
			t.Fatalf("a CWNSB pool is offered as a source: %+v", s)
		}
	}

	if _, err := l.ops.importCopy(ctx, ImportCopyArgs{UUID: src.UUID, Pool: pool, Share: share}); errCode(err) != CodeImportNotMounted {
		t.Fatalf("copy before mount: %v", err)
	}
	m, merr := l.ops.importMount(ctx, ImportMountArgs{UUID: src.UUID})
	if merr != nil || !strings.Contains(strings.Join(m.Entries, ","), "Photos") {
		t.Fatalf("mount = %+v %v", m, merr)
	}
	if info := l.ops.mountInfo(ctx, m.MountPoint); !strings.Contains(","+info.Options+",", ",ro,") {
		t.Fatalf("source not read-only: %+v", info)
	}
	if _, err := l.ops.importCopy(ctx, ImportCopyArgs{UUID: src.UUID, Pool: pool, Share: share, Subdir: "../etc"}); errCode(err) != CodeInvalidArgs {
		t.Fatalf("escape from the source: %v", err)
	}
	out, cerr := l.ops.importCopy(ctx, ImportCopyArgs{UUID: src.UUID, Pool: pool, Share: share})
	if cerr != nil || !strings.HasPrefix(out["dest"], "Import OMVDATA ") {
		t.Fatalf("copy = %v %v", out, cerr)
	}
	if _, err := l.ops.importUnmount(ctx, ImportMountArgs{UUID: src.UUID}); err != nil && errCode(err) != CodeJobRunning {
		t.Fatalf("unmount during copy: %v", err)
	}
	var p ImportProgress
	for i := 0; i < 120; i++ {
		p, _ = l.ops.importProgress(ctx, ImportShareArgs{Share: share})
		if p.Job.State == "succeeded" || p.Job.State == "failed" {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if p.Job.State != "succeeded" || p.TotalBytes == 0 || p.CopiedBytes != p.TotalBytes || p.Files != 2 || p.Dest != out["dest"] {
		t.Fatalf("progress = %+v", p)
	}

	dest := filepath.Join(shares.SharePath(pool, share), out["dest"])
	b, rerr := os.ReadFile(filepath.Join(dest, "Photos/2024/beach.jpg"))
	if rerr != nil || sha256.Sum256(b) != sum {
		t.Fatalf("copied file differs: %v", rerr)
	}
	st, _ := os.Stat(filepath.Join(dest, "Photos/2024/beach.jpg"))
	if st.Mode().Perm() != 0o660 {
		t.Fatalf("file mode %v, want 0660", st.Mode().Perm())
	}
	dst, _ := os.Stat(filepath.Join(dest, "Photos"))
	if dst.Mode()&os.ModeSetgid == 0 || dst.Mode().Perm() != 0o770 {
		t.Fatalf("dir mode %v, want setgid 2770", dst.Mode())
	}
	if g := l.getent("group", shares.ShareGroup(share)); !strings.HasPrefix(g, shares.ShareGroup(share)+":") {
		t.Fatal(g)
	}

	if _, err := l.ops.importUnmount(ctx, ImportMountArgs{UUID: src.UUID}); err != nil {
		t.Fatalf("unmount: %v", err)
	}
	if _, err := os.Stat(importer.MountPoint(src.UUID)); !os.IsNotExist(err) {
		t.Fatalf("mount point left: %v", err)
	}
	// The source was never written: its ext4 is clean and unchanged.
	if res, _ := l.run.Run(ctx, "e2fsck", "-n", md); res.ExitCode != 0 {
		t.Fatalf("source filesystem check: %s", res.Stdout)
	}
}
