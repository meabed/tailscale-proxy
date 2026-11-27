package core

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Local HTTPS names: `tsp run` starts an app on a free port and registers
// <name> → port here; `tsp proxy` serves https://<name>.localhost from it.
// Each route is <name>.json in ~/.tsp/routes, written atomically by
// its `tsp run`, which holds <name>.lock for as long as it runs. The lock is the
// proof of life: the OS drops it when the run ends in any way (kill -9, a crash,
// a reboot), so a route without a held lock is dead and is removed, under that
// same lock, by whoever reads it next. Claiming the lock is also what makes a
// name taken: two runs can never hold one name.

const (
	localTLD     = "localhost"
	localPortMin = 4000 // app ports `tsp run` hands out: localPortMin..localPortMax
	localPortMax = 4999
)

// localRoute is one registered local name.
type localRoute struct {
	Name string `json:"name"` // e.g. "workspace-api.help-ai" → https://workspace-api.help-ai.localhost
	Port int    `json:"port"` // the app's port on 127.0.0.1
	PID  int    `json:"pid"`  // the `tsp run` process that owns the route
	Dir  string `json:"dir"`  // where it was started (shown in lists)
}

// URL is the route's HTTPS address behind `tsp proxy` on 443.
func (r localRoute) URL() string { return "https://" + r.Name + "." + localTLD }

var nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

// validateName accepts lowercase DNS labels joined by dots ("api", "workspace-api.help-ai").
func validateName(name string) error {
	if len(name) > 200 || !nameRe.MatchString(name) {
		return fmt.Errorf("invalid name %q: use lowercase letters, digits and dashes, labels joined by dots (e.g. workspace-api.help-ai)", name)
	}
	if strings.HasSuffix(name, "."+localTLD) || name == localTLD {
		return fmt.Errorf("invalid name %q: leave out .%s (tsp adds it)", name, localTLD)
	}
	return nil
}

// nameFromHost maps a request host ("api.localhost:443") to a route name ("api").
// The bare "localhost" and hosts outside .localhost yield "".
func nameFromHost(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	name, ok := strings.CutSuffix(host, "."+localTLD)
	if !ok {
		return ""
	}
	return name
}

// localRegistry reads and writes the route files in one directory. Lookups are
// served from memory and re-read only when the directory changes.
type localRegistry struct {
	dir string

	mu     sync.Mutex
	mod    time.Time
	routes map[string]localRoute
}

func newLocalRegistry(dir string) *localRegistry { return &localRegistry{dir: dir} }

func (r *localRegistry) file(name string) string { return filepath.Join(r.dir, name+".json") }
func (r *localRegistry) lock(name string) string { return filepath.Join(r.dir, name+".lock") }

// claim takes name for this process (ok=false: a live run holds it). The caller
// keeps it until it exits; release() gives it up.
func (r *localRegistry) claim(name string) (release func(), ok bool, err error) {
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return nil, false, fmt.Errorf("creating %s: %v", r.dir, err)
	}
	return tryLock(r.lock(name))
}

// add writes the route atomically. The caller holds its claim.
func (r *localRegistry) add(rt localRoute) error {
	data, err := json.Marshal(rt)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(r.file(rt.Name), data, 0o600); err != nil {
		return fmt.Errorf("writing route %s: %v", rt.Name, err)
	}
	return nil
}

// remove deletes the route; only its owner (the claim holder) calls it.
func (r *localRegistry) remove(name string) { _ = os.Remove(r.file(name)) }

// read parses name's route file; ok=false when it is missing or invalid.
func (r *localRegistry) read(name string) (localRoute, bool) {
	data, err := os.ReadFile(r.file(name))
	if err != nil {
		return localRoute{}, false
	}
	var rt localRoute
	if json.Unmarshal(data, &rt) != nil || validateName(rt.Name) != nil || rt.Port <= 0 {
		return localRoute{}, false
	}
	return rt, true
}

// alive reports whether a run holds name. A free lock means its run is gone: the
// route file is removed while the lock is held here, so no new run can be
// writing it at that moment.
func (r *localRegistry) alive(name string) bool {
	release, free, err := tryLock(r.lock(name))
	if err != nil {
		return false
	}
	if !free {
		return true
	}
	r.remove(name)
	release()
	return false
}

// list returns the live routes sorted by name, removing dead and unreadable ones.
func (r *localRegistry) list() []localRoute {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return nil
	}
	var out []localRoute
	for _, e := range entries {
		name, isRoute := strings.CutSuffix(e.Name(), ".json")
		if e.IsDir() || !isRoute || validateName(name) != nil {
			continue
		}
		rt, ok := r.read(name)
		if !r.alive(name) || !ok || rt.Name != name {
			continue // dead (now removed), or unreadable/mislabelled while its run lives: never delete another run's file
		}
		out = append(out, rt)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// lookup resolves a name from the cached map, re-reading the directory when its
// modification time moved (a run started, stopped, or a dead route was dropped).
// The time is taken before the read: a route added during the read moves it
// again, so the next lookup sees it.
func (r *localRegistry) lookup(name string) (localRoute, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fi, err := os.Stat(r.dir)
	if err != nil {
		r.routes, r.mod = nil, time.Time{}
		return localRoute{}, false
	}
	if r.routes == nil || !fi.ModTime().Equal(r.mod) {
		r.mod = fi.ModTime()
		r.routes = map[string]localRoute{}
		for _, rt := range r.list() {
			r.routes[rt.Name] = rt
		}
	}
	rt, ok := r.routes[name]
	return rt, ok
}

// forget re-checks a cached route that stopped answering: if its run is gone,
// the route is dropped and the cache re-read on the next lookup.
func (r *localRegistry) forget(rt localRoute) {
	if r.alive(rt.Name) {
		return
	}
	r.mu.Lock()
	r.routes = nil
	r.mu.Unlock()
}

// waitLock retries tryLock on path for up to d (ok=false: still held).
func waitLock(path string, d time.Duration) (release func(), ok bool, err error) {
	deadline := time.Now().Add(d)
	for {
		release, ok, err = tryLock(path)
		if err != nil || ok || time.Now().After(deadline) {
			return release, ok, err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// pickPort returns a free app port for name: a stable one derived from the name
// (the same app gets the same port run after run), else the next free one.
func pickPort(name string, taken map[int]bool, free func(int) bool) (int, error) {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	span := localPortMax - localPortMin + 1
	start := int(h.Sum32() % uint32(span))
	for i := 0; i < span; i++ {
		p := localPortMin + (start+i)%span
		if !taken[p] && free(p) {
			return p, nil
		}
	}
	return 0, fmt.Errorf("no free port in %d-%d", localPortMin, localPortMax)
}

// portFree reports whether 127.0.0.1:p can be listened on right now.
func portFree(p int) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}
