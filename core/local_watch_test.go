package core

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// fakeWatcher finds whatever found() returns, counting scans.
func fakeWatcher(t *testing.T, reg *localRegistry, found func() []Service) (*localWatcher, *atomic.Int64) {
	t.Helper()
	var scans atomic.Int64
	w := &localWatcher{reg: reg, enabled: true}
	w.store = newRouteStore(func() ([]Service, []duplicate, error) {
		scans.Add(1)
		return found(), nil, nil
	}, 2, false)
	return w, &scans
}

func TestLocalWatcher_aNewServerAnswersOnTheFirstRequest(t *testing.T) {
	saved := rescanMinGap
	rescanMinGap = 50 * time.Millisecond
	t.Cleanup(func() { rescanMinGap = saved })
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("hi from " + r.Host)) }))
	defer up.Close()
	port, _ := strconv.Atoi(up.URL[len("http://127.0.0.1:"):])

	reg := testRegistry(t)
	var started atomic.Bool
	w, scans := fakeWatcher(t, reg, func() []Service {
		if !started.Load() {
			return nil
		}
		return []Service{{Slug: "web", Name: "web", Port: port, PID: 9, Dir: "/src/web", Runtime: "node"}}
	})
	h := newLocalHandler(t.TempDir(), reg, w, newProxyMetrics(), "errors", everyOption)
	w.scan()

	started.Store(true) // the server starts after the last scan
	rec := get(h, "web.localhost", "/", "")
	if rec.Code != 200 || rec.Body.String() != "hi from web.localhost" {
		t.Fatalf("first request: %d %q (scans %d)", rec.Code, rec.Body.String(), scans.Load())
	}
	// A burst of unknown names, side by side, scans at most once per rescanMinGap.
	before, start := scans.Load(), time.Now()
	done := make(chan struct{})
	for range 20 {
		go func() { get(h, "nope.localhost", "/", ""); done <- struct{}{} }()
	}
	for range 20 {
		<-done
	}
	if n, most := scans.Load()-before, int64(time.Since(start)/rescanMinGap)+1; n > most {
		t.Errorf("20 requests for unknown names ran %d scans in %s (at most %d)", n, time.Since(start), most)
	}
}

func TestLocalWatcher_tspRunWinsOverAFoundName(t *testing.T) {
	run := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("run")) }))
	defer run.Close()
	runPort := run.Listener.Addr().(*net.TCPAddr).Port
	reg := testRegistry(t)
	running(t, reg, localRoute{Name: "api", Port: runPort, PID: 1, Dir: "/src/api"})
	w, _ := fakeWatcher(t, reg, func() []Service {
		return []Service{{Slug: "api", Name: "api", Port: 1, PID: 2}, {Slug: "docs", Name: "docs", Port: 2, PID: 3}}
	})
	w.scan()
	h := newLocalHandler(t.TempDir(), reg, w, newProxyMetrics(), "errors", everyOption)
	if rec := get(h, "api.localhost", "/", ""); rec.Body.String() != "run" {
		t.Errorf("api: %d %q, want the tsp run app", rec.Code, rec.Body.String())
	}
	all := allRoutes(reg, w)
	if len(all) != 2 || all[0].Name != "api" || all[0].Source != sourceRun || all[1].Source != sourceFound {
		t.Errorf("allRoutes = %+v", all)
	}
}

func TestLocalWatcher_offFindsNothing(t *testing.T) {
	reg := testRegistry(t)
	w, scans := fakeWatcher(t, reg, func() []Service { return []Service{{Slug: "web", Name: "web", Port: 1}} })
	w.enabled = false
	w.rescanSoon()
	if _, ok := w.lookup("web"); ok || scans.Load() != 0 || w.routes() != nil {
		t.Error("discovery off still found a name")
	}
}

func TestLocalWatcher_scansSlowlyWhenIdle(t *testing.T) {
	saved := []time.Duration{watchEvery, watchIdle, watchActiveFor}
	watchEvery, watchIdle, watchActiveFor = 10*time.Millisecond, 150*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() { watchEvery, watchIdle, watchActiveFor = saved[0], saved[1], saved[2] })
	w, scans := fakeWatcher(t, testRegistry(t), func() []Service { return nil })
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { w.loop(ctx); close(stopped) }()
	defer func() { cancel(); <-stopped }() // before the cleanup restores the timings
	time.Sleep(200 * time.Millisecond)
	idle := scans.Load()
	if idle > 3 {
		t.Errorf("idle for 200ms: %d scans, want about 2 (every %s)", idle, watchIdle)
	}
	w.touch()
	time.Sleep(100 * time.Millisecond)
	if busy := scans.Load() - idle; busy < 4 {
		t.Errorf("in use for 100ms: %d scans, want about 10 (every %s)", busy, watchEvery)
	}
}
