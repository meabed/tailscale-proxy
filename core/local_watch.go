package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// The local proxy finds dev servers by itself: the same discovery `tsp start` runs
// (listeners in the port range whose process is a web runtime, grouped by project
// root, named by the project's config or its folder), so any dev server answers at
// https://<name>.localhost with no tsp run and no change to its scripts. Found
// routes live in memory; a tsp run route with the same name wins.
//
// It scans every watchEvery while the names get requests (watchIdle when nothing
// asked for watchActiveFor: an idle laptop pays one lsof a minute), and at once (at
// most once a second) when a request asks for a name nothing serves yet, so a server
// started a moment ago answers on the first try. A server that stops is dropped
// after the same debounce as tsp start.

// Watcher timings; shortened in tests.
var (
	watchEvery     = 10 * time.Second
	watchIdle      = 60 * time.Second
	watchActiveFor = 2 * time.Minute
	rescanMinGap   = time.Second
	rescanWaitMost = 1500 * time.Millisecond
)

// runPorts is where tsp run puts apps: always scanned, so the range covers them.
var runPorts = portRange{Lo: 4000, Hi: 4999}

// localWatcher keeps the found routes for the local proxy.
type localWatcher struct {
	reg     *localRegistry
	disc    *discoverer
	store   *routeStore // keyed by local name
	enabled bool
	asked   atomic.Int64 // unix nanos of the last request for a name

	mu       sync.Mutex
	inflight chan struct{} // closed when the running scan ends
	pending  chan struct{} // the scan requests for unknown names wait on
	last     time.Time     // when the last scan started
	lastErr  string
	scans    int
}

// newLocalWatcher reads the user's config: discovery on or off, the port range,
// runtimes, docker, and the debounce.
func newLocalWatcher(reg *localRegistry, r Runner) *localWatcher {
	cfg, _, _, _ := LoadConfig()
	w := &localWatcher{reg: reg, disc: newDiscoverer(r), enabled: cfg.Discover}
	rng, err := parsePortRange(cfg.Ports)
	if err != nil {
		rng = runPorts
	}
	rng = portRange{Lo: min(rng.Lo, runPorts.Lo), Hi: max(rng.Hi, runPorts.Hi)}
	dcfg := discoverConfig{rng: rng, all: cfg.All, runtimes: parseRuntimes(cfg.Runtimes), docker: cfg.Docker, local: true}
	w.store = newRouteStore(func() ([]Service, []duplicate, error) { return w.found(dcfg) }, cfg.DeregisterCycles, false)
	return w
}

// found is one scan: discovered services keyed by local name, leaving out the
// ports tsp run apps hold (they have their names already).
func (w *localWatcher) found(dcfg discoverConfig) ([]Service, []duplicate, error) {
	svcs, dups, err := w.disc.discover(dcfg)
	if err != nil {
		return nil, nil, err
	}
	runPortsNow := map[int]bool{}
	for _, rt := range w.reg.list() {
		runPortsNow[rt.Port] = true
	}
	out := svcs[:0]
	for _, svc := range svcs {
		if runPortsNow[svc.Port] {
			continue
		}
		svc.Slug = svc.Name // the store is keyed by name here
		out = append(out, svc)
	}
	return out, dups, nil
}

// scan runs one discovery, or waits for the one already running.
func (w *localWatcher) scan() {
	w.mu.Lock()
	if ch := w.inflight; ch != nil {
		w.mu.Unlock()
		<-ch
		return
	}
	ch := make(chan struct{})
	w.inflight, w.last = ch, time.Now()
	w.mu.Unlock()

	added, _, removed, err := w.store.refresh()

	w.mu.Lock()
	w.inflight = nil
	w.scans++
	w.lastErr = ""
	if err != nil {
		w.lastErr = err.Error()
	}
	w.mu.Unlock()
	close(ch)
	if err != nil {
		appLog().Warn("discovery failed", "err", err)
	}
	for _, svc := range added {
		appLog().Info("found", "name", svc.Name, "port", svc.Port, "dir", svc.Dir)
	}
	for _, name := range removed {
		appLog().Info("gone", "name", name)
	}
}

// loop scans until ctx ends (nothing when discovery is off).
func (w *localWatcher) loop(ctx context.Context) {
	if !w.enabled {
		return
	}
	w.scan()
	t := time.NewTicker(watchEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.mu.Lock()
			since := time.Since(w.last)
			w.mu.Unlock()
			if w.busy() || since >= watchIdle {
				w.scan()
			}
		}
	}
}

// touch records a request for a name: scans speed up while they come.
func (w *localWatcher) touch() {
	if w != nil {
		w.asked.Store(time.Now().UnixNano())
	}
}

// busy reports whether a name was asked for within watchActiveFor.
func (w *localWatcher) busy() bool {
	return time.Since(time.Unix(0, w.asked.Load())) < watchActiveFor
}

// lookup returns the found route for name.
func (w *localWatcher) lookup(name string) (localRoute, bool) {
	if w == nil || !w.enabled {
		return localRoute{}, false
	}
	svc, ok := w.store.lookup(name)
	if !ok {
		return localRoute{}, false
	}
	return routeOf(svc), true
}

// rescanSoon looks again for a name nothing serves yet. Every request that asks
// joins one scheduled scan, which starts once rescanMinGap has passed since the
// last one (the server may have started a moment after it): at most one scan per
// gap, however many requests wait. A request waits at most rescanMinGap +
// rescanWaitMost.
func (w *localWatcher) rescanSoon() {
	if w == nil || !w.enabled {
		return
	}
	w.mu.Lock()
	ch := w.pending
	if ch == nil {
		ch = make(chan struct{})
		w.pending = ch
		wait := rescanMinGap - time.Since(w.last)
		go func() {
			if wait > 0 {
				time.Sleep(wait)
			}
			w.scan()
			w.mu.Lock()
			w.pending = nil
			w.mu.Unlock()
			close(ch)
		}()
	}
	w.mu.Unlock()
	select {
	case <-ch:
	case <-time.After(rescanMinGap + rescanWaitMost):
	}
}

// forget drops a found name whose process tsp just stopped, so lists show it
// gone now, not after the de-register debounce.
func (w *localWatcher) forget(name string) {
	if w != nil && w.enabled {
		w.store.forget(name)
	}
}

// routes are the found routes, by name.
func (w *localWatcher) routes() []localRoute {
	if w == nil || !w.enabled {
		return nil
	}
	var out []localRoute
	for _, svc := range w.store.snapshot() {
		out = append(out, routeOf(svc))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func routeOf(svc Service) localRoute {
	return localRoute{Name: svc.Name, Port: svc.Port, PID: svc.PID, Dir: svc.Dir, Source: sourceFound, Runtime: svc.Runtime, Host: svc.upstreamHost()}
}

// watchStatus is the watcher as `tsp routes`, doctor and the dashboard show it.
type watchStatus struct {
	Enabled   bool         `json:"enabled"`
	Every     string       `json:"every"`
	Range     string       `json:"range"`
	LastScan  time.Time    `json:"lastScan,omitzero"`
	Scans     int          `json:"scans"`
	LastError string       `json:"lastError,omitempty"`
	Problems  []string     `json:"configProblems,omitempty"` // project config files left out, and why
	Routes    []localRoute `json:"routes"`
}

func (w *localWatcher) status() watchStatus {
	if w == nil {
		return watchStatus{Routes: []localRoute{}}
	}
	w.mu.Lock()
	st := watchStatus{Enabled: w.enabled, Every: watchEvery.String() + " while in use, " + watchIdle.String() + " idle", LastScan: w.last, Scans: w.scans, LastError: w.lastErr}
	w.mu.Unlock()
	if cfg, _, _, _ := LoadConfig(); cfg.Ports != "" {
		if rng, err := parsePortRange(cfg.Ports); err == nil {
			st.Range = fmt.Sprintf("%d-%d", min(rng.Lo, runPorts.Lo), max(rng.Hi, runPorts.Hi))
		}
	}
	st.Problems = projectConfigProblems()
	st.Routes = w.routes()
	if st.Routes == nil {
		st.Routes = []localRoute{}
	}
	return st
}

// fetchWatchStatus asks the serving proxy for its watcher's state.
func fetchWatchStatus(dir string, pid int) (watchStatus, error) {
	var st watchStatus
	resp, err := adminClient(dir, pid, 3*time.Second).Get("http://tsp/routes")
	if err != nil {
		return st, err
	}
	defer resp.Body.Close()
	return st, json.NewDecoder(resp.Body).Decode(&st)
}
