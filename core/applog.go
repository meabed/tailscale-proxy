package core

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// The proxies' event log: JSON lines (log/slog) in ~/.tsp/logs/proxy.jsonl, at
// most logMaxBytes, then rotated to proxy.jsonl.1 and .2. Several processes write
// it (the supervisor and, during a swap, two workers): writes are O_APPEND lines,
// rotation runs under logs/rotate.lock, and a writer reopens the file when another
// one rotated it. `tsp logs` reads it.

const (
	logMaxBytes = 5 << 20
	logKeep     = 3
)

func proxyLogPath(dir string) string { return filepath.Join(dir, "logs", "proxy.jsonl") }

var (
	appLogMu  sync.Mutex
	appLogger *slog.Logger
)

// appLog is the process's event logger; discarded until initAppLog runs.
func appLog() *slog.Logger {
	appLogMu.Lock()
	defer appLogMu.Unlock()
	if appLogger == nil {
		return slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	return appLogger
}

// initAppLog sends appLog to dir's proxy.jsonl, every line tagged with role
// ("supervisor", "worker", "proxy") and this process's pid and release.
func initAppLog(dir, role string) {
	w := newRotatingFile(proxyLogPath(dir), logMaxBytes, logKeep)
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo})
	appLogMu.Lock()
	appLogger = slog.New(h).With("role", role, "pid", os.Getpid(), "version", Version)
	appLogMu.Unlock()
}

// rotatingFile appends lines to path, keeping keep files of at most max bytes.
type rotatingFile struct {
	mu      sync.Mutex
	path    string
	max     int64
	keep    int
	f       *os.File
	size    int64
	checked time.Time
}

func newRotatingFile(path string, max int64, keep int) *rotatingFile {
	return &rotatingFile{path: path, max: max, keep: keep}
}

func (r *rotatingFile) open() error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	r.f, r.size, r.checked = f, fi.Size(), time.Now()
	return nil
}

// Write appends p (one line), rotating first when the file is full.
func (r *rotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f != nil && time.Since(r.checked) > 5*time.Second {
		// Another process may have rotated the file: follow the name, not the old inode.
		r.checked = time.Now()
		if cur, err := os.Stat(r.path); err != nil || !sameInode(r.f, cur) {
			_ = r.f.Close()
			r.f = nil
		} else {
			r.size = cur.Size()
		}
	}
	if r.f == nil {
		if err := r.open(); err != nil {
			return 0, err
		}
	}
	if r.size+int64(len(p)) > r.max {
		r.rotate()
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

// rotate shifts path → path.1 → … under a lock shared with the other writers.
func (r *rotatingFile) rotate() {
	release, ok, err := tryLock(filepath.Join(filepath.Dir(r.path), "rotate.lock"))
	if err == nil && ok {
		if fi, err := os.Stat(r.path); err == nil && fi.Size()+1 > r.max/2 {
			for i := r.keep - 1; i >= 1; i-- {
				from := r.path
				if i > 1 {
					from += "." + strconv.Itoa(i-1)
				}
				_ = os.Rename(from, r.path+"."+strconv.Itoa(i))
			}
		}
		release()
	}
	_ = r.f.Close()
	r.f = nil
	_ = r.open()
}

func (r *rotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}

func sameInode(f *os.File, cur os.FileInfo) bool {
	fi, err := f.Stat()
	return err == nil && os.SameFile(fi, cur)
}

// rotatedPaths lists path's files oldest first (path.2, path.1, path).
func rotatedPaths(path string, keep int) []string {
	out := make([]string, 0, keep)
	for i := keep - 1; i >= 1; i-- {
		out = append(out, path+"."+strconv.Itoa(i))
	}
	return append(out, path)
}
