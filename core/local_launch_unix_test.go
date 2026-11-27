//go:build !windows

package core

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestInheritedListeners_roundTrip(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	f, err := ln.(*net.TCPListener).File()
	if err != nil {
		t.Fatal(err)
	}
	// A raw fd, as an exec leaves it: inheritedListeners owns and closes it (an
	// *os.File here would close the number again, maybe another test's file by then).
	fd, err := syscall.Dup(int(f.Fd()))
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(envListenFDs, strconv.Itoa(fd))
	t.Setenv(envListenPort, "443")
	t.Setenv(envLauncher, "1.12.0")

	lns, port, launcher, ok, err := inheritedListeners()
	if err != nil || !ok || port != 443 || launcher != "1.12.0" || len(lns) != 1 {
		t.Fatalf("lns=%d port=%d launcher=%q ok=%v err=%v", len(lns), port, launcher, ok, err)
	}
	defer lns[0].Close()
	if os.Getenv(envListenFDs) != "" || os.Getenv(envLauncher) != "" {
		t.Error("the handoff variables stay in the environment for the apps it starts")
	}
	go func() {
		c, err := net.Dial("tcp", ln.Addr().String())
		if err == nil {
			_, _ = c.Write([]byte("hi"))
			_ = c.Close()
		}
	}()
	c, err := lns[0].Accept()
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(c)
	if string(got) != "hi" {
		t.Errorf("read %q through the inherited socket", got)
	}
}

func TestInheritedListeners_none(t *testing.T) {
	t.Setenv(envListenFDs, "")
	if _, _, _, ok, err := inheritedListeners(); ok || err != nil {
		t.Errorf("ok=%v err=%v without a handoff", ok, err)
	}
	t.Setenv(envListenFDs, "1")
	if _, _, _, ok, err := inheritedListeners(); !ok || err == nil {
		t.Error("a stdio fd was accepted as a listener")
	}
}

// handOff must return (and leave the launcher serving) whenever the copy cannot run.
func TestHandOff_refusesWhatCannotRun(t *testing.T) {
	dir := t.TempDir()
	withVersion(t, "1.13.0")
	if err := handOff(nil, dir, 443); err == nil || !strings.Contains(err.Error(), "no tsp at") {
		t.Errorf("no copy: %v", err)
	}
	script := filepath.Join(t.TempDir(), "tsp")
	_ = os.WriteFile(script, []byte("#!/bin/sh\necho 1.12.0\n"), 0o755)
	_ = installProxyBin(dir, script, "1.12.0")
	if err := handOff(nil, dir, 443); err == nil || !strings.Contains(err.Error(), "older than this launcher") {
		t.Errorf("older copy: %v", err)
	}
	_ = os.WriteFile(script, []byte("#!/bin/sh\nexit 3\n"), 0o755)
	_ = installProxyBin(dir, script, "1.14.0")
	if err := handOff(nil, dir, 443); err == nil || !strings.Contains(err.Error(), "--version") {
		t.Errorf("broken copy: %v", err)
	}
}

func TestProbeBinary(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "tsp")
	_ = os.WriteFile(bin, []byte("#!/bin/sh\necho v1.14.0\n"), 0o755)
	if err := probeBinary(bin, "1.14.0"); err != nil {
		t.Errorf("a working copy: %v", err)
	}
	if err := probeBinary(bin, "1.15.0"); err == nil {
		t.Error("a copy of another release passed")
	}
}
