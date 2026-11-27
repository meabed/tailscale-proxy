//go:build !windows

package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// tryLock takes an exclusive, non-blocking lock on path (created if missing).
// ok=false: another process holds it. The kernel drops the lock when its holder
// exits for any reason (kill -9, a crash, a reboot), so a held lock is proof of
// a live owner and a pid in a file is never trusted on its own.
//
// A lock file may be removed by its holder (removeLocked), so a lock taken on a
// file that is no longer the one at path proves nothing: it is dropped and
// taken again on the file that is there now.
func tryLock(path string) (release func(), ok bool, err error) {
	for {
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
		if err != nil {
			return nil, false, err
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			_ = f.Close()
			if errors.Is(err, syscall.EWOULDBLOCK) {
				return nil, false, nil
			}
			return nil, false, err
		}
		held, herr := f.Stat()
		current, cerr := os.Stat(path)
		if herr == nil && cerr == nil && os.SameFile(held, current) {
			return func() { _ = f.Close() }, true, nil
		}
		_ = f.Close() // removed or replaced while we locked it: lock the file at path now
	}
}

// removeLocked deletes path, whose lock the caller holds, then gives the lock up:
// removed first, so no process can lock this file and believe it owns path.
func removeLocked(path string, release func()) {
	_ = os.Remove(path)
	release()
}

// checkRootOnly refuses a path a non-root user could replace: every existing
// folder above it must belong to root and be writable by root alone (a sticky
// bit is fine). The root service runs that file.
func checkRootOnly(path string) error {
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		if fi, err := os.Stat(dir); err == nil {
			st, ok := fi.Sys().(*syscall.Stat_t)
			if !ok || st.Uid != 0 || fi.Mode().Perm()&0o022 != 0 {
				return fmt.Errorf("%s must belong to root and be writable only by root (the boot service runs %s as root)", dir, path)
			}
		}
		if parent := filepath.Dir(dir); parent == dir {
			return nil
		}
	}
}
