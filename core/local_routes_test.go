package core

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"api", "workspace-api.help-ai", "a1.b2.c3", "x"} {
		if err := validateName(ok); err != nil {
			t.Errorf("validateName(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "API", "-api", "api-", "a..b", "a_b", "api.localhost", "localhost", "a b", "../x", string(make([]byte, 201))} {
		if err := validateName(bad); err == nil {
			t.Errorf("validateName(%q) = nil, want an error", bad)
		}
	}
}

func TestNameFromHost(t *testing.T) {
	cases := map[string]string{
		"api.localhost":                      "api",
		"API.LocalHost:443":                  "api",
		"workspace-api.help-ai.localhost.":   "workspace-api.help-ai",
		"workspace-api.help-ai.localhost:80": "workspace-api.help-ai",
		"localhost":                          "",
		"localhost:443":                      "",
		"example.com":                        "",
		"127.0.0.1:443":                      "",
	}
	for host, want := range cases {
		if got := nameFromHost(host); got != want {
			t.Errorf("nameFromHost(%q) = %q, want %q", host, got, want)
		}
	}
}

func TestLocalRouteURL(t *testing.T) {
	if got := (LocalRoute{Name: "api.acme"}).URL(); got != "https://api.acme.localhost" {
		t.Errorf("URL() = %q", got)
	}
}

// testRegistry is an empty registry in a temp dir.
func testRegistry(t *testing.T) *localRegistry {
	t.Helper()
	reg := newLocalRegistry(filepath.Join(t.TempDir(), "routes"))
	if err := os.MkdirAll(reg.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return reg
}

// running registers rt the way its `tsp run` does (claim, then the route file)
// and returns the function that ends that run's claim.
func running(t *testing.T, reg *localRegistry, rt LocalRoute) (stop func()) {
	t.Helper()
	release, ok, err := reg.claim(rt.Name)
	if err != nil || !ok {
		t.Fatalf("claim(%s) = %v, %v", rt.Name, ok, err)
	}
	if err := reg.add(rt); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	return release
}

// liveRoute finds name among the registry's live routes.
func liveRoute(reg *localRegistry, name string) (LocalRoute, bool) {
	for _, rt := range reg.list() {
		if rt.Name == name {
			return rt, true
		}
	}
	return LocalRoute{}, false
}

func TestRegistry_claimIsExclusive(t *testing.T) {
	reg := testRegistry(t)
	release, ok, err := reg.claim("api")
	if err != nil || !ok {
		t.Fatalf("first claim = %v, %v", ok, err)
	}
	if _, ok, _ := reg.claim("api"); ok {
		t.Fatal("a second claim on a held name succeeded")
	}
	if _, ok, _ := reg.claim("other"); !ok {
		t.Fatal("a claim on another name failed")
	}
	release()
	again, ok, _ := reg.claim("api")
	if !ok {
		t.Fatal("the name stayed taken after its claim was released")
	}
	again()
}

func TestRegistry_addGetListRemove(t *testing.T) {
	reg := testRegistry(t)
	running(t, reg, LocalRoute{Name: "b", Port: 4002, PID: 20, Dir: "/b"})
	running(t, reg, LocalRoute{Name: "a", Port: 4001, PID: 10, Dir: "/a"})
	got := reg.list()
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "b" {
		t.Fatalf("list() = %+v, want a then b", got)
	}
	if rt, ok := liveRoute(reg, "a"); !ok || rt.Port != 4001 || rt.Dir != "/a" {
		t.Errorf("get(a) = %+v, %v", rt, ok)
	}
	reg.remove("a")
	if _, ok := liveRoute(reg, "a"); ok {
		t.Error("get(a) found a removed route")
	}
	if _, ok := liveRoute(reg, "missing"); ok {
		t.Error("get(missing) found a route")
	}
}

func TestRegistry_addReplacesAtomically(t *testing.T) {
	reg := testRegistry(t)
	running(t, reg, LocalRoute{Name: "a", Port: 4001, PID: 1})
	_ = reg.add(LocalRoute{Name: "a", Port: 4009, PID: 2})
	if rt, _ := liveRoute(reg, "a"); rt.Port != 4009 || rt.PID != 2 {
		t.Errorf("get(a) = %+v, want the second write", rt)
	}
	entries, _ := os.ReadDir(reg.dir)
	for _, e := range entries {
		if e.Name() != "a.json" && e.Name() != "a.lock" {
			t.Errorf("left behind: %s", e.Name())
		}
	}
}

func TestRegistry_deadRunsAreDropped(t *testing.T) {
	reg := testRegistry(t)
	running(t, reg, LocalRoute{Name: "live", Port: 4001, PID: 1})
	stop := running(t, reg, LocalRoute{Name: "dead", Port: 4002, PID: 2})
	stop()                                                                                       // its run ended without removing the file (kill -9)
	_ = os.WriteFile(reg.file("orphan"), []byte(`{"name":"orphan","port":4003,"pid":3}`), 0o644) // never claimed

	got := reg.list()
	if len(got) != 1 || got[0].Name != "live" {
		t.Fatalf("list() = %+v, want only live", got)
	}
	for _, name := range []string{"dead", "orphan"} {
		if _, err := os.Stat(reg.file(name)); !os.IsNotExist(err) {
			t.Errorf("%s.json was not removed", name)
		}
	}
	if _, ok := liveRoute(reg, "dead"); ok {
		t.Error("get(dead) = true")
	}
	if _, ok, _ := reg.claim("dead"); !ok {
		t.Error("a dead run's name cannot be claimed")
	}
}

func TestRegistry_liveRunsFilesAreNeverDeleted(t *testing.T) {
	// While a run holds its name, an unreadable or mislabelled file of its is skipped, not deleted.
	reg := testRegistry(t)
	running(t, reg, LocalRoute{Name: "a", Port: 4001, PID: 1})
	_ = os.WriteFile(reg.file("a"), []byte("{"), 0o644)
	if got := reg.list(); len(got) != 0 {
		t.Errorf("list() = %+v", got)
	}
	if _, err := os.Stat(reg.file("a")); err != nil {
		t.Error("a live run's file was deleted")
	}
	running(t, reg, LocalRoute{Name: "b", Port: 4002, PID: 1})
	_ = os.WriteFile(reg.file("b"), []byte(`{"name":"other","port":4002,"pid":1}`), 0o644)
	if got := reg.list(); len(got) != 0 {
		t.Errorf("a mislabelled file was listed: %+v", got)
	}
}

func TestRegistry_listSkipsOtherFiles(t *testing.T) {
	reg := testRegistry(t)
	for _, f := range []string{"notes.txt", "BAD_NAME.json", ".route-123"} {
		_ = os.WriteFile(filepath.Join(reg.dir, f), []byte("x"), 0o644)
	}
	_ = os.Mkdir(filepath.Join(reg.dir, "sub.json"), 0o755)
	if got := reg.list(); len(got) != 0 {
		t.Errorf("list() = %+v", got)
	}
	for _, f := range []string{"notes.txt", "BAD_NAME.json", ".route-123", "sub.json"} {
		if _, err := os.Stat(filepath.Join(reg.dir, f)); err != nil {
			t.Errorf("%s was removed", f)
		}
	}
}

func TestRegistry_listMissingDir(t *testing.T) {
	if got := newLocalRegistry(filepath.Join(t.TempDir(), "none")).list(); got != nil {
		t.Errorf("list() of a missing dir = %+v, want nil", got)
	}
}

func TestRegistry_readRejectsInvalid(t *testing.T) {
	reg := testRegistry(t)
	for name, body := range map[string]string{
		"noport": `{"name":"noport","pid":1}`,
		"badjs":  `{`,
	} {
		_ = os.WriteFile(reg.file(name), []byte(body), 0o644)
		if _, ok := reg.read(name); ok {
			t.Errorf("read(%s) accepted %s", name, body)
		}
	}
}

var bumps int

// bump moves dir's mtime forward (directory mtimes can be coarse) so a change is seen.
func bump(t *testing.T, dir string) {
	t.Helper()
	bumps++
	future := time.Now().Add(time.Duration(bumps) * time.Minute)
	if err := os.Chtimes(dir, future, future); err != nil {
		t.Fatal(err)
	}
}

func TestRegistry_lookupFollowsChanges(t *testing.T) {
	reg := testRegistry(t)
	if _, ok := reg.lookup("a"); ok {
		t.Fatal("lookup in an empty registry found a route")
	}
	stopA := running(t, reg, LocalRoute{Name: "a", Port: 4001, PID: 1})
	bump(t, reg.dir)
	if rt, ok := reg.lookup("a"); !ok || rt.Port != 4001 {
		t.Fatalf("lookup(a) = %+v, %v after add", rt, ok)
	}
	running(t, reg, LocalRoute{Name: "b", Port: 4002, PID: 2})
	bump(t, reg.dir)
	if _, ok := reg.lookup("b"); !ok {
		t.Fatal("lookup(b) missed a route added after the first read")
	}
	reg.remove("a")
	stopA()
	bump(t, reg.dir)
	if _, ok := reg.lookup("a"); ok {
		t.Fatal("lookup(a) still found a removed route")
	}
	if _, ok := newLocalRegistry(filepath.Join(t.TempDir(), "none")).lookup("a"); ok {
		t.Fatal("lookup in a missing dir found a route")
	}
}

func TestRegistry_forget(t *testing.T) {
	reg := testRegistry(t)
	rt := LocalRoute{Name: "a", Port: 4001, PID: 1}
	stop := running(t, reg, rt)
	if _, ok := reg.lookup("a"); !ok {
		t.Fatal("lookup(a) missed")
	}
	reg.forget(rt) // its run is alive: kept
	if _, ok := reg.lookup("a"); !ok {
		t.Fatal("forget dropped a live route")
	}
	stop()
	reg.forget(rt)
	if _, err := os.Stat(reg.file("a")); !os.IsNotExist(err) {
		t.Fatal("forget kept a dead run's file")
	}
	if _, ok := reg.lookup("a"); ok {
		t.Fatal("lookup(a) still served a forgotten route")
	}
}

func TestWaitLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	held, _, _ := tryLock(path)
	if _, ok, err := waitLock(path, 150*time.Millisecond); ok || err != nil {
		t.Fatalf("waitLock on a held lock = %v, %v", ok, err)
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		held()
	}()
	release, ok, err := waitLock(path, 5*time.Second)
	if !ok || err != nil {
		t.Fatalf("waitLock after release = %v, %v", ok, err)
	}
	release()
}

func TestPickPort(t *testing.T) {
	all := func(int) bool { return true }
	p1, err := pickPort("api.acme", nil, all)
	if err != nil || p1 < localPortMin || p1 > localPortMax {
		t.Fatalf("pickPort = %d, %v", p1, err)
	}
	if p2, _ := pickPort("api.acme", nil, all); p2 != p1 {
		t.Errorf("pickPort is not stable: %d then %d", p1, p2)
	}
	if next, _ := pickPort("api.acme", map[int]bool{p1: true}, all); next == p1 {
		t.Error("pickPort returned a taken port")
	}
	if busy, _ := pickPort("api.acme", nil, func(p int) bool { return p != p1 }); busy == p1 {
		t.Error("pickPort returned a busy port")
	}
	if _, err := pickPort("api.acme", nil, func(int) bool { return false }); err == nil {
		t.Error("pickPort with no free port returned no error")
	}
	if wrap, _ := pickPort("x", nil, func(p int) bool { return p == localPortMin }); wrap != localPortMin {
		t.Errorf("pickPort did not wrap around to %d: %d", localPortMin, wrap)
	}
}

func TestPortFree(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if portFree(port) {
		t.Errorf("portFree(%d) = true while listening", port)
	}
	_ = ln.Close()
	if !portFree(port) {
		t.Errorf("portFree(%d) = false after close", port)
	}
}
