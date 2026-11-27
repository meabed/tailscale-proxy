//go:build !windows

package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrivateRunDir(t *testing.T) {
	dir := filepath.Join(shortHome(t), ".tsp")
	if err := privateRunDir(dir); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(filepath.Join(dir, "run")); fi.Mode().Perm() != 0o700 {
		t.Errorf("run/ is %v, want 0700", fi.Mode().Perm())
	}
	_ = os.Chmod(filepath.Join(dir, "run"), 0o777) // loosened: tightened again
	if err := privateRunDir(dir); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(filepath.Join(dir, "run")); fi.Mode().Perm() != 0o700 {
		t.Errorf("a 0777 run/ stayed %v", fi.Mode().Perm())
	}
	// A run/ that is a link (to a folder another user controls) is refused.
	other := t.TempDir()
	_ = os.RemoveAll(filepath.Join(dir, "run"))
	_ = os.Symlink(other, filepath.Join(dir, "run"))
	if err := privateRunDir(dir); err == nil {
		t.Error("a symlinked run/ was accepted")
	}
}

func TestAdminSocket_privateAndShortOnly(t *testing.T) {
	dir := filepath.Join(shortHome(t), ".tsp")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go adminServer(ctx, dir, newProxyMetrics)
	path, err := adminSocketPath(dir, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	var fi os.FileInfo
	for i := 0; i < 100; i++ {
		if fi, err = os.Stat(path); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket %s: %v, mode %v", path, err, fi.Mode().Perm())
	}
	if _, err := fetchLiveStats(dir, os.Getpid()); err != nil {
		t.Errorf("stats over the socket: %v", err)
	}
	if _, err := adminSocketPath("/"+strings.Repeat("d", 120), 1); err == nil {
		t.Error("a path too long for a unix socket was accepted")
	}
}
