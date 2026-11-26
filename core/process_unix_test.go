//go:build !windows

package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestCheckRootOnly(t *testing.T) {
	if err := checkRootOnly("/usr/bin/tsp-test-none"); err != nil {
		t.Errorf("/usr/bin: %v", err)
	}
	if err := checkRootOnly(filepath.Join(t.TempDir(), "x", "tsp")); err == nil {
		t.Error("a user's temp dir passed the root-only check")
	}
}

func TestTryLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	release, ok, err := tryLock(path)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, ok, _ := tryLock(path); ok {
		t.Error("a second lock on the same file succeeded")
	}
	release()
	if r, ok, _ := tryLock(path); !ok {
		t.Error("the lock stayed after release")
	} else {
		r()
	}
	if _, _, err := tryLock(filepath.Join(t.TempDir(), "missing", "x.lock")); err == nil {
		t.Error("tryLock in a missing folder returned no error")
	}
}

func TestStopProcess(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := stopProcess(cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Error("the process exited cleanly instead of on SIGTERM")
	}
}

func TestDropPrivileges_needsRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root")
	}
	if err := dropPrivileges(501, 20); err == nil {
		t.Error("dropPrivileges as non-root returned no error")
	}
}

func TestRunChild_forwardsTerm(t *testing.T) {
	// SIGTERM to tsp reaches the app; the app's exit code comes back.
	done := make(chan int, 1)
	go func() {
		done <- runChild([]string{"sh", "-c", "trap 'exit 7' TERM; while :; do sleep 0.05; done"}, os.Environ(), func(c *exec.Cmd) {
			go func() {
				time.Sleep(200 * time.Millisecond)
				_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
			}()
		})
	}()
	select {
	case code := <-done:
		if code != 7 {
			t.Errorf("exit = %d, want 7 (the app's TERM trap)", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the app never got SIGTERM")
	}
}

func TestRunChild_signalExit(t *testing.T) {
	code := runChild([]string{"sh", "-c", "kill -9 $$"}, os.Environ(), nil)
	if code != 1 {
		t.Errorf("killed app → %d, want 1", code)
	}
}
