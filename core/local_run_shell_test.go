//go:build !windows

package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The shell form expands $PORT in the app's environment, and passes the exit code.
func TestRunChild_shellCommand(t *testing.T) {
	out := filepath.Join(t.TempDir(), "port")
	env := runEnv(os.Environ(), 4321, "", func(string) string { return "x" })
	if code := runChild(commandArgv([]string{"printf %s:%s $PORT $HOST > " + out}), env); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got, _ := os.ReadFile(out); string(got) != "4321:127.0.0.1" {
		t.Errorf("the command saw %q", got)
	}
	if code := runChild(commandArgv([]string{"exit 3"}), env); code != 3 {
		t.Errorf("exit 3 → %d", code)
	}
}

// A simple command replaces the shell, so a SIGTERM to tsp stops the app itself.
func TestRunChild_shellCommandGetsSignals(t *testing.T) {
	if os.Getenv("TSP_SIGNAL_CHILD") == "1" {
		os.Exit(runChild(commandArgv([]string{"sleep 30"}), os.Environ()))
	}
	exe, _ := os.Executable()
	cmd := exec.Command(exe, "-test.run=TestRunChild_shellCommandGetsSignals$")
	cmd.Env = append(os.Environ(), "TSP_SIGNAL_CHILD=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("tsp run did not stop the shell command on SIGTERM")
	}
	if out, _ := exec.Command("pgrep", "-f", "sleep 30").Output(); strings.TrimSpace(string(out)) != "" {
		for _, pid := range strings.Fields(string(out)) {
			_ = exec.Command("kill", pid).Run()
		}
		t.Error("the app outlived tsp run")
	}
}
