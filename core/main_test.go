package core

import (
	"os"
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
