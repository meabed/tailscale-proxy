package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCmdRoutes(t *testing.T) {
	home, _ := isolate(t)
	if code := cmdRoutes(nil); code != 0 {
		t.Errorf("empty = %d", code)
	}
	if code := cmdRoutes([]string{"--json"}); code != 0 {
		t.Errorf("empty --json = %d", code)
	}
	reg := newLocalRegistry(filepath.Join(home, ".tsp", "routes"))
	_ = reg.add(localRoute{Name: "api", Port: 4001, PID: os.Getpid(), Dir: "/src/api"})
	if code := cmdRoutes(nil); code != 0 {
		t.Errorf("text = %d", code)
	}
	if code := cmdRoutes([]string{"--json"}); code != 0 {
		t.Errorf("--json = %d", code)
	}
	if code := cmdRoutes([]string{"--bogus"}); code != 2 {
		t.Errorf("bad flag = %d, want 2", code)
	}
}
