//go:build !windows

package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestParseLsofListeners(t *testing.T) {
	// lsof -nP -iTCP -sTCP:LISTEN -Fpcn  (p=pid, c=command, n=name)
	out := "p4231\ncbun\nn*:4295\nn[::1]:4295\n" +
		"p630\ncControlCe\nn*:5000\n" +
		"p999\ncnode\nn127.0.0.1:8080\n" // 8080 out of range
	rng := PortRange{Lo: 3000, Hi: 5000}
	ls := parseLsofListeners(out, rng)
	// 4295 (deduped across v4/v6), 5000; not 8080
	if len(ls) != 2 {
		t.Fatalf("got %d listeners: %+v", len(ls), ls)
	}
	byPort := map[int]listener{}
	for _, l := range ls {
		byPort[l.Port] = l
	}
	if byPort[4295].PID != 4231 || byPort[4295].Comm != "bun" {
		t.Errorf("4295 wrong: %+v", byPort[4295])
	}
	if _, ok := byPort[5000]; !ok {
		t.Errorf("expected port 5000")
	}
}

func TestParsePsComm(t *testing.T) {
	out := "  4231 /opt/homebrew/bin/bun\n  630 /System/.../ControlCenter\n"
	m := parsePsComm(out)
	if m[4231] != "/opt/homebrew/bin/bun" {
		t.Errorf("4231 = %q", m[4231])
	}
}

func TestParseLsofCwd(t *testing.T) {
	out := "p4231\nn/Users/me/work/help-ai/services/agent\np630\nn/\n"
	m := parseLsofCwd(out)
	if m[4231] != "/Users/me/work/help-ai/services/agent" {
		t.Errorf("4231 cwd = %q", m[4231])
	}
}

func TestParseDockerListeners_hostBoundAndInternalFallback(t *testing.T) {
	body := []byte(`[
		{
			"Names":["/web"],
			"Ports":[
				{"PrivatePort":3000,"PublicPort":49153},
				{"PrivatePort":8080,"PublicPort":0}
			],
			"NetworkSettings":{"Networks":{"bridge":{"IPAddress":"172.17.0.2"}}}
		},
		{
			"Names":["/worker"],
			"Ports":[{"PrivatePort":9000,"PublicPort":0}],
			"NetworkSettings":{"Networks":{"bridge":{"IPAddress":""}}}
		}
	]`)

	ls := parseDockerListeners(body, PortRange{Lo: 3000, Hi: 50000})
	if len(ls) != 2 {
		t.Fatalf("got %d listeners: %+v", len(ls), ls)
	}
	if ls[0].Port != 49153 || ls[0].Host != "127.0.0.1" || ls[0].Cwd != "web" {
		t.Fatalf("host-bound listener wrong: %+v", ls[0])
	}
	if ls[1].Port != 8080 || ls[1].Host != "172.17.0.2" || ls[1].Cwd != "web" {
		t.Fatalf("internal fallback listener wrong: %+v", ls[1])
	}
	if ls[0].PID == ls[1].PID {
		t.Fatalf("docker ports need distinct synthetic PIDs, got %+v", ls)
	}
}

func TestDockerListenersBuildServices_keepsMultipleContainerPorts(t *testing.T) {
	ls := []listener{
		{Port: 3000, Host: "172.17.0.2", PID: -1, Comm: "docker", Cwd: "web"},
		{Port: 8080, Host: "172.17.0.2", PID: -2, Comm: "docker", Cwd: "web"},
	}

	svcs, dups := buildServices(ls, false, nil)
	if len(svcs) != 2 {
		t.Fatalf("got %d services: %+v", len(svcs), svcs)
	}
	if len(dups) != 1 {
		t.Fatalf("got %d duplicate groups: %+v", len(dups), dups)
	}
	if svcs[0].Host != "172.17.0.2" || svcs[1].Host != "172.17.0.2" {
		t.Fatalf("service hosts not preserved: %+v", svcs)
	}
}

func TestDockerListenerContainerNamesRemainDistinctProjects(t *testing.T) {
	ls := []listener{
		{Port: 3000, Host: "127.0.0.1", PID: -1, Comm: "docker", Cwd: "web-api"},
		{Port: 8090, Host: "127.0.0.1", PID: -2, Comm: "docker", Cwd: "model-server"},
	}

	svcs, dups := buildServices(ls, false, nil)
	if len(svcs) != 2 {
		t.Fatalf("got %d services: %+v", len(svcs), svcs)
	}
	if len(dups) != 0 {
		t.Fatalf("distinct containers should not be duplicates: %+v", dups)
	}
	bySlug := map[string]Service{}
	for _, svc := range svcs {
		bySlug[svc.Slug] = svc
	}
	if _, ok := bySlug["web-api"]; !ok {
		t.Fatalf("missing web-api slug: %+v", svcs)
	}
	if _, ok := bySlug["model-server"]; !ok {
		t.Fatalf("missing model-server slug: %+v", svcs)
	}
}

// lsofRunner fakes the discovery commands: the listening-socket lsof, the cwd
// lsof, and ps. It counts calls so tests can assert what a scan spawns.
type lsofRunner struct {
	listen, cwd, ps string
	cwdErr          error
	calls           map[string]int
}

func (r *lsofRunner) Run(name string, args ...string) (string, string, error) {
	if r.calls == nil {
		r.calls = map[string]int{}
	}
	key := name
	if name == "lsof" && strings.Contains(strings.Join(args, " "), "cwd") {
		key = "lsof-cwd"
	}
	r.calls[key]++
	switch key {
	case "lsof":
		return r.listen, "", nil
	case "lsof-cwd":
		return r.cwd, "", r.cwdErr
	case "ps":
		return r.ps, "", nil
	}
	return "", "unexpected", errString("unexpected " + name)
}

// fakeDevBox returns a runner describing n node servers, each in its own project.
func fakeDevBox(t testing.TB, n int) *lsofRunner {
	root := t.TempDir()
	var listen, cwd, ps strings.Builder
	for i := 0; i < n; i++ {
		pid := 1000 + i
		dir := filepath.Join(root, "proj-"+strconv.Itoa(i), "apps", "web")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "proj-"+strconv.Itoa(i), "package.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&listen, "p%d\ncnode\nf20\nn*:%d\nf21\nn[::1]:%d\n", pid, 3000+i, 3000+i)
		fmt.Fprintf(&cwd, "p%d\nfcwd\nn%s\n", pid, dir)
		fmt.Fprintf(&ps, "%d /opt/homebrew/bin/node\n", pid)
	}
	return &lsofRunner{listen: listen.String(), cwd: cwd.String(), ps: ps.String()}
}

// A re-scan of unchanged servers spawns only the listener lsof: ps and the cwd
// lookup ran once, and node is classified by lsof's command so ps never runs.
func TestDiscover_cachesProcessLookups(t *testing.T) {
	r := fakeDevBox(t, 3)
	d := newDiscoverer(r)
	cfg := discoverConfig{rng: PortRange{3000, 6000}}
	first, _, err := d.Discover(cfg)
	if err != nil || len(first) != 3 || first[0].Slug != "proj-0" {
		t.Fatalf("first scan: %+v, %v", first, err)
	}
	second, _, _ := d.Discover(cfg)
	if fmt.Sprint(first) != fmt.Sprint(second) {
		t.Fatalf("re-scan changed services:\n%+v\n%+v", first, second)
	}
	if r.calls["lsof"] != 2 || r.calls["lsof-cwd"] != 1 || r.calls["ps"] != 0 {
		t.Fatalf("calls = %v, want 2 listener scans, 1 cwd lookup, no ps", r.calls)
	}
}

// ps runs only for a process lsof can't classify (e.g. a `go run` binary), and
// a PID that disappears is evicted; one that comes back under another command
// is looked up again.
func TestDiscover_psFallbackAndEviction(t *testing.T) {
	dir := mkProject(t, t.TempDir(), "gosvc")
	r := &lsofRunner{
		listen: "p77\ncmain\nn*:4000\n",
		cwd:    "p77\nfcwd\nn" + dir + "\n",
		ps:     "77 /var/folders/x/go-build1/b001/exe/main\n",
	}
	d := newDiscoverer(r)
	cfg := discoverConfig{rng: PortRange{3000, 6000}}
	svcs, _, _ := d.Discover(cfg)
	if len(svcs) != 1 || svcs[0].Runtime != "go" || svcs[0].Slug != "gosvc" {
		t.Fatalf("got %+v", svcs)
	}
	if r.calls["ps"] != 1 {
		t.Fatalf("ps calls = %d, want 1", r.calls["ps"])
	}
	r.listen = ""
	d.Discover(cfg)
	if len(d.procs) != 0 {
		t.Fatalf("gone PID not evicted: %+v", d.procs)
	}
	r.listen = "p77\ncnode\nn*:4000\n" // PID reused by another program
	svcs, _, _ = d.Discover(cfg)
	if len(svcs) != 1 || svcs[0].Runtime != "node" || r.calls["lsof-cwd"] != 2 {
		t.Fatalf("reused PID not re-resolved: %+v, calls %v", svcs, r.calls)
	}
}

// lsof exits non-zero when one PID can't be read but still prints the others;
// those cwds must be used (a single unreadable process used to drop them all).
func TestDiscover_usesPartialCwdOutput(t *testing.T) {
	r := fakeDevBox(t, 2)
	r.cwdErr = errString("exit status 1")
	svcs, _, _ := newDiscoverer(r).Discover(discoverConfig{rng: PortRange{3000, 6000}})
	if len(svcs) != 2 || svcs[0].Slug != "proj-0" || svcs[1].Slug != "proj-1" {
		t.Fatalf("got %+v", svcs)
	}
}

func TestLsofPorts(t *testing.T) {
	if got := lsofPorts(PortRange{3000, 6000}); got != "3000-6000" {
		t.Errorf("range = %q", got)
	}
	if got := lsofPorts(PortRange{4000, 4000}); got != "4000" {
		t.Errorf("single = %q", got)
	}
}

// BenchmarkDiscover_steadyState measures one re-scan of 20 unchanged servers
// (the 20s poll), excluding the cost of the spawned commands themselves.
func BenchmarkDiscover_steadyState(b *testing.B) {
	d := newDiscoverer(fakeDevBox(b, 20))
	cfg := discoverConfig{rng: PortRange{3000, 6000}}
	if _, _, err := d.Discover(cfg); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		d.Discover(cfg)
	}
}
