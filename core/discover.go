package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Service is one discovered listening dev server.
type Service struct {
	Slug    string // URL path segment (tsp start), from the project's path or folder
	Name    string // its local name: https://<name>.localhost (the project's name, else the folder slug)
	Share   string // the project's share setting: "", "off", "private", "public"
	Port    int    // upstream port
	Host    string // upstream host/IP; empty means 127.0.0.1
	Runtime string // node|bun|deno or "" (unknown)
	Dir     string // working directory (may be "")
	PID     int
}

// portRange is an inclusive TCP port range.
type portRange struct{ Lo, Hi int }

func (r portRange) contains(p int) bool { return p >= r.Lo && p <= r.Hi }

// discoverConfig bundles the discovery filters.
type discoverConfig struct {
	rng      portRange
	all      bool
	runtimes map[string]bool // nil = all known web runtimes
	docker   bool            // also query Docker API for containers
	local    bool            // for local names: projects with "share": false stay in
}

// listener is a raw OS-level listening socket (pre-classification). Comm is the
// command name from lsof/netstat (the interpreter, e.g. "node"); PsComm is the
// process's own name from `ps` (e.g. "http-server", or a "go-build" temp path).
type listener struct {
	Port   int
	Host   string
	PID    int
	Comm   string
	PsComm string
	Cwd    string
	Root   string // project root dir of Cwd (cached per process); "" = resolve from Cwd
}

// runtimeByExe maps a process's executable basename to a runtime label. These
// are the runtimes discovered by default; --all includes everything, --runtimes
// restricts to a subset. Server wrappers (uvicorn, puma, …) map to their language.
var runtimeByExe = map[string]string{
	// JavaScript / TypeScript
	"node": "node", "bun": "bun", "deno": "deno",
	// Python (interpreter + common app servers)
	"python": "python", "python2": "python", "python3": "python",
	"uvicorn": "python", "gunicorn": "python", "hypercorn": "python",
	"flask": "python", "waitress-serve": "python",
	// Ruby (interpreter + common app servers)
	"ruby": "ruby", "puma": "ruby", "unicorn": "ruby", "rackup": "ruby",
	"rails": "ruby", "thin": "ruby",
	// PHP
	"php": "php", "php-fpm": "php",
	// Go (compiled binaries are undetectable; `go run` is caught by heuristic below)
	"go": "go",
	// JVM
	"java": "java",
	// .NET
	"dotnet": "dotnet",
	// Elixir / Erlang
	"beam": "elixir", "beam.smp": "elixir",
	// Perl
	"perl": "perl",
	// Docker-published ports (the userland forwarder serves the host port)
	"docker-proxy": "docker", "com.docker.backend": "docker",
	"vpnkit-forwarder": "docker", "vpnkit": "docker",
}

// projectMarkers identify a project root when walking up from a process's cwd.
var projectMarkers = []string{
	".tsp.json", ".git",
	"package.json", "deno.json", "deno.jsonc", "bun.lockb",
	"go.mod",
	"pyproject.toml", "requirements.txt", "Pipfile", "setup.py",
	"Gemfile",
	"composer.json",
	"Cargo.toml",
	"pom.xml", "build.gradle", "build.gradle.kts",
	"mix.exs",
}

// runtimeOf classifies a listener by its lsof command first (the reliable
// interpreter name, e.g. "node" even when the process renamed itself to
// "http-server"), falling back to the ps process name (which carries the
// "go-build" temp path that catches `go run`).
func runtimeOf(l listener) string {
	if rt := classifyRuntime(l.Comm); rt != "" {
		return rt
	}
	return classifyRuntime(l.PsComm)
}

// classifyRuntime maps an executable path to a runtime label, or "" if unknown.
func classifyRuntime(comm string) string {
	base := strings.ToLower(filepath.Base(comm))
	base = strings.TrimSuffix(base, ".exe")
	if rt, ok := runtimeByExe[base]; ok {
		return rt
	}
	switch {
	case strings.HasPrefix(base, "python"): // python3.12, python3.13, …
		return "python"
	case strings.Contains(comm, "go-build"): // `go run` temp binary
		return "go"
	case strings.Contains(base, "docker"): // docker-proxy variants
		return "docker"
	}
	return ""
}

// KnownRuntimes returns the distinct runtime labels discovery recognizes,
// sorted (the startup banner, the desktop settings).
func KnownRuntimes() []string {
	set := map[string]bool{}
	for _, label := range runtimeByExe {
		set[label] = true
	}
	out := make([]string, 0, len(set))
	for label := range set {
		out = append(out, label)
	}
	sort.Strings(out)
	return out
}

// projectRootDir walks up from dir to the nearest directory containing a project
// marker and returns it; dir itself when there is none. The walk stops at the
// user's home and at the filesystem root, and never enters a folder anyone can
// write (/tmp): a project's config is read only from folders its owner controls.
func projectRootDir(dir string) string {
	if dir == "" || dir == "/" {
		return ""
	}
	if !filepath.IsAbs(dir) {
		return dir
	}
	home, _ := os.UserHomeDir()
	for d := dir; ; {
		fi, err := os.Stat(d)
		if err != nil || worldWritable(fi) {
			break
		}
		for _, m := range projectMarkers {
			if _, err := os.Stat(filepath.Join(d, m)); err == nil {
				return d
			}
		}
		parent := filepath.Dir(d)
		if parent == d || d == home {
			break
		}
		d = parent
	}
	return dir
}

var slugUnsafe = regexp.MustCompile(`[^a-z0-9]+`)

// slugify normalizes a name into a URL path segment.
func slugify(name string) string {
	s := strings.ToLower(name)
	s = slugUnsafe.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// duplicate records a project folder that exposes more than one service (more
// than one distinct process listening in it). Members holds them all, main first.
type duplicate struct {
	Slug    string
	Members []Service // all services for this project, main (clean slug) first
}

// serviceOf builds a Service from a raw listener under a given slug and name.
func serviceOf(l listener, slug, name, share string) Service {
	host := l.Host
	if host == "" {
		host = "127.0.0.1"
	}
	return Service{Slug: slug, Name: name, Share: share, Port: l.Port, Host: host, Runtime: runtimeOf(l), Dir: l.Cwd, PID: l.PID}
}

// configProblems are the project config files the last scan could not use
// (file → what is wrong): their projects are left out until they are fixed.
var configProblems atomic.Pointer[map[string]string]

// projectConfigProblems returns the last scan's problems, sorted by file.
func projectConfigProblems() []string {
	m := configProblems.Load()
	if m == nil {
		return nil
	}
	out := make([]string, 0, len(*m))
	for _, msg := range *m {
		out = append(out, msg)
	}
	sort.Strings(out)
	return out
}

// projectBaseSlug derives the clean project slug from the project root, or
// "<runtime>-<port>" / "port-<port>" when there's no discoverable project.
func projectBaseSlug(l listener) string {
	if l.Root != "" {
		if slug := slugify(filepath.Base(l.Root)); slug != "" {
			return slug
		}
	}
	if rt := classifyRuntime(l.Comm); rt != "" {
		return rt + "-" + strconv.Itoa(l.Port)
	}
	return "port-" + strconv.Itoa(l.Port)
}

// buildServices turns raw listeners into routable services.
//
//   - Listeners are grouped by project-root directory.
//   - Multiple ports from the SAME process (e.g. a dev server + its HMR port)
//     collapse to that process's lowest port — one entry per process.
//   - Within a project, the process on the LOWEST port is the "main" service and
//     gets the clean project slug; every other distinct process is kept too, with
//     a "-<port>" suffix (so an auxiliary tool in the same folder stays reachable).
//   - Distinct projects that share a folder name get a "-<port>" suffix on their
//     main slug to stay unique.
//
// Projects exposing more than one service are returned as Duplicates for display.
func buildServices(listeners []listener, includeAll bool, runtimes map[string]bool) ([]Service, []duplicate) {
	// Group surviving listeners by project-root dir (ungroupable → unique key).
	groups := map[string][]listener{}
	order := []string{}
	for _, l := range listeners {
		rt := runtimeOf(l)
		if !includeAll && (rt == "" || (runtimes != nil && !runtimes[rt])) {
			continue
		}
		if l.Root == "" {
			l.Root = projectRootDir(l.Cwd)
		}
		key := l.Root
		if key == "" {
			key = "\x00port:" + strconv.Itoa(l.Port)
		}
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], l)
	}

	// Per project: one entry per process (its lowest port), sorted by port; the
	// project's config (loadProjectConfig) names it, picks its main port, or leaves it out.
	type project struct {
		procs []listener // distinct processes; procs[0] is "main"
		slug  string     // base (clean) path slug
		name  string     // base local name
		share string
	}
	projects := make([]project, 0, len(order))
	slugCounts, nameCounts := map[string]int{}, map[string]int{}
	problems := map[string]string{}
	for _, key := range order {
		byPID := map[int]listener{}
		pidOrder := []int{}
		for _, l := range groups[key] {
			if cur, ok := byPID[l.PID]; !ok {
				byPID[l.PID] = l
				pidOrder = append(pidOrder, l.PID)
			} else if l.Port < cur.Port {
				byPID[l.PID] = l
			}
		}
		procs := make([]listener, 0, len(pidOrder))
		for _, pid := range pidOrder {
			procs = append(procs, byPID[pid])
		}
		sort.Slice(procs, func(i, j int) bool { return procs[i].Port < procs[j].Port })
		cfg, skipped, err := loadProjectConfig(procs[0].Root)
		for _, why := range skipped {
			problems[why] = why
		}
		if err != nil {
			problems[procs[0].Root] = err.Error()
			continue
		}
		if !cfg.Enabled {
			continue
		}
		if cfg.Port > 0 { // the configured port is the app: it goes first
			for i, proc := range procs {
				if proc.Port == cfg.Port {
					procs[0], procs[i] = procs[i], procs[0]
					break
				}
			}
		}
		folder := projectBaseSlug(procs[0])
		name := cfg.Name
		if name == "" {
			name = folder
		}
		p := project{procs: procs, slug: cfg.pathSlug(folder), name: name, share: cfg.Share}
		projects = append(projects, p)
		slugCounts[p.slug]++
		nameCounts[p.name]++
	}
	if prev := configProblems.Swap(&problems); prev != nil {
		for k, msg := range problems {
			if _, seen := (*prev)[k]; !seen {
				appLog().Warn("project config skipped", "why", msg)
			}
		}
	}

	var services []Service
	var dups []duplicate
	for _, p := range projects {
		mainSlug, mainName := p.slug, p.name
		if slugCounts[mainSlug] > 1 { // distinct projects share a folder name
			mainSlug = mainSlug + "-" + strconv.Itoa(p.procs[0].Port)
		}
		if nameCounts[mainName] > 1 {
			mainName = mainName + "-" + strconv.Itoa(p.procs[0].Port)
		}
		members := make([]Service, 0, len(p.procs))
		for i, proc := range p.procs {
			slug, name := mainSlug, mainName
			if i > 0 { // not the main process → suffix to stay reachable
				slug = mainSlug + "-" + strconv.Itoa(proc.Port)
				name = mainName + "-" + strconv.Itoa(proc.Port)
			}
			svc := serviceOf(proc, slug, name, p.share)
			members = append(members, svc)
			services = append(services, svc)
		}
		if len(members) > 1 {
			dups = append(dups, duplicate{Slug: mainSlug, Members: members})
		}
	}
	sort.Slice(services, func(i, j int) bool { return services[i].Slug < services[j].Slug })
	sort.Slice(dups, func(i, j int) bool { return dups[i].Slug < dups[j].Slug })
	return services, dups
}

// parsePortRange parses "lo-hi" or a single "port" into a validated portRange.
func parsePortRange(s string) (portRange, error) {
	s = strings.TrimSpace(s)
	// Single port, e.g. "4000" -> {4000, 4000}.
	if !strings.Contains(s, "-") {
		p, err := strconv.Atoi(s)
		if err != nil || p < 1 || p > 65535 {
			return portRange{}, fmt.Errorf("invalid port %q", s)
		}
		return portRange{Lo: p, Hi: p}, nil
	}
	parts := strings.SplitN(s, "-", 2)
	lo, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	hi, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || lo < 1 || hi < lo || hi > 65535 {
		return portRange{}, fmt.Errorf("invalid port range %q (want lo-hi or a single port)", s)
	}
	return portRange{Lo: lo, Hi: hi}, nil
}

// parseRuntimes turns "node,bun" into a set; "" yields nil (all known).
func parseRuntimes(s string) map[string]bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	m := map[string]bool{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			m[p] = true
		}
	}
	return m
}

// discoverer lists services using an injected command Runner (for exec parsers).
// It remembers what it learned about each listening process by PID, so a
// steady-state re-scan spawns only the listener listing; PIDs that stop
// listening are evicted every scan, so the cache never outgrows the live set.
type discoverer struct {
	run   Runner
	mu    sync.Mutex // one scan at a time: guards procs, no overlapping lsof runs
	procs map[int]procInfo
}

// procInfo is what discovery resolved for one listening process. comm is the
// command the listener listing reported; a PID that comes back under another
// command was reused and is resolved again.
type procInfo struct{ comm, psComm, cwd, root string }

func newDiscoverer(r Runner) *discoverer { return &discoverer{run: r, procs: map[int]procInfo{}} }

// discover returns the filtered services in the configured range, plus any
// duplicate-instance info (same project on multiple ports).
func (d *discoverer) discover(cfg discoverConfig) ([]Service, []duplicate, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	ls, err := d.listeners(cfg.rng)
	if err != nil {
		return nil, nil, err
	}
	if cfg.docker {
		ls = mergeListeners(ls, dockerListeners(cfg.rng))
	}
	svcs, dups := buildServices(ls, cfg.all, cfg.runtimes)
	if !cfg.local { // `tsp start` leaves out the projects that say "share": false
		kept := svcs[:0]
		for _, svc := range svcs {
			if svc.Share != "off" {
				kept = append(kept, svc)
			}
		}
		svcs = kept
	}
	return svcs, dups, nil
}

// mergeListeners appends the Docker listeners on ports the host listing does not
// already cover (the host's listener wins).
func mergeListeners(host, docker []listener) []listener {
	covered := make(map[int]bool, len(host))
	for _, l := range host {
		covered[l.Port] = true
	}
	for _, l := range docker {
		if !covered[l.Port] {
			host = append(host, l)
			covered[l.Port] = true
		}
	}
	return host
}

// portFromAddr extracts the port from a listener address ("*:4764",
// "127.0.0.1:3000", "[::1]:3000"); 0 if there is none.
func portFromAddr(addr string) int {
	i := strings.LastIndexByte(addr, ':')
	if i < 0 {
		return 0
	}
	p, err := strconv.Atoi(addr[i+1:])
	if err != nil {
		return 0
	}
	return p
}
