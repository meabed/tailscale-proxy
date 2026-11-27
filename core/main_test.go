package core

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"testing"
)

// TestMain gives every test a throwaway HOME, so none can read or move a real
// ~/.tsp. Run with TSP_TEST_AS_TSP=1, the test binary is `tsp <args>` instead: the
// supervisor tests start it as a real worker.
func TestMain(m *testing.M) {
	if os.Getenv("TSP_TEST_AS_TSP") == "1" {
		os.Exit(Run(os.Args[1:]))
	}
	if mode := os.Getenv("TSP_TEST_LISTEN"); mode != "" {
		listenForever(mode == "stubborn")
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

// listenForever is a dev server for the stop tests: it prints its port and
// serves until it is stopped; a stubborn one ignores SIGTERM and SIGINT.
func listenForever(stubborn bool) {
	if stubborn {
		signal.Ignore(syscall.SIGTERM, os.Interrupt)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.Exit(1)
	}
	fmt.Println(ln.Addr().(*net.TCPAddr).Port)
	_ = http.Serve(ln, http.NotFoundHandler())
	os.Exit(0)
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
