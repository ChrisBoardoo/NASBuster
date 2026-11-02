// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build integration && linux

package helper

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/crosswax/nasbuster/internal/shares"
	"github.com/crosswax/nasbuster/internal/storage"
)

func TestLabRecyclePurge(t *testing.T) {
	ctx := context.Background()
	l := newLabPool(t, 2)
	pool, _, err := l.create(storage.LayoutMirror, 0, 1)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	share := "itrecycle"
	if _, err := l.ops.shareCreate(ctx, ShareCreateArgs{Pool: pool, Name: share}); err != nil {
		t.Fatalf("share: %v", err)
	}
	t.Cleanup(func() { l.run.Run(ctx, "groupdel", shares.ShareGroup(share)) })
	bin := filepath.Join(shares.SharePath(pool, share), shares.RecycleDir)
	old := filepath.Join(bin, "alice", "Docs", "old.txt")
	fresh := filepath.Join(bin, "alice", "fresh.txt")
	outside := filepath.Join(shares.SharePath(pool, share), "keep.txt")
	os.MkdirAll(filepath.Dir(old), 0o770)
	for _, f := range []string{old, fresh, outside} {
		os.WriteFile(f, []byte("x"), 0o660)
	}
	past := time.Now().Add(-40 * 24 * time.Hour)
	os.Chtimes(old, past, past)
	os.Chtimes(outside, past, past)
	// A link in the bin to a file outside must not be followed.
	os.Symlink(outside, filepath.Join(bin, "alice", "link"))

	if _, err := l.ops.recyclePurge(ctx, RecyclePurgeArgs{Pool: pool, Share: share, Days: 30}); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("a file deleted 40 days ago is still in the bin")
	}
	if _, err := os.Stat(filepath.Dir(old)); !os.IsNotExist(err) {
		t.Fatal("empty folder left in the bin")
	}
	for _, f := range []string{fresh, outside} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("%s removed: %v", f, err)
		}
	}
	if _, err := l.ops.recyclePurge(ctx, RecyclePurgeArgs{Pool: pool, Share: share, Days: 0}); errCode(err) != CodeInvalidArgs {
		t.Fatalf("0 days: %v", err)
	}
}
