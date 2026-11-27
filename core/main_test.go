package core

import (
	"os"
	"runtime"
	"testing"
)

// TestMain gives every test a throwaway HOME, so none can read or move a real
// ~/.tsp. Run with TSP_TEST_AS_TSP=1, the test binary is `tsp <args>` instead: the
// supervisor tests start it as a real worker.
func TestMain(m *testing.M) {
	if os.Getenv("TSP_TEST_AS_TSP") == "1" {
		os.Exit(Run(os.Args[1:]))
	}
	home, err := os.MkdirTemp("", "tsp-test-home-")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("HOME", home)
	_ = os.Setenv("USERPROFILE", home)
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}

// shortHome is a HOME short enough for a unix socket under ~/.tsp/run.
func shortHome(t *testing.T) string {
	t.Helper()
	base := "/tmp"
	if runtime.GOOS == "windows" {
		base = os.TempDir()
	}
	home, err := os.MkdirTemp(base, "tsp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}
