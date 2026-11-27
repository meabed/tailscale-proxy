package core

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Metrics for the proxies, cheap enough to leave on: per name (a .localhost name,
// or a project slug behind Tailscale) atomic counters and a latency histogram of
// fixed buckets, so the request path takes no lock beyond a sync.Map read. A
// running proxy answers `tsp stats` over a unix socket (adminServer) and appends a
// one-minute summary to ~/.tsp/logs/stats.jsonl (historyLoop), so the history
// outlives restarts and swaps.

// The names metrics never grow past: unregistered names count as one, and a
// machine running more than maxRouteKeys names counts the rest together.
const (
	maxRouteKeys = 256
	unknownRoute = "(unknown)"
	otherRoutes  = "(other)"
)

// latencyBoundsMs are the histogram's upper bounds; the last bucket is "slower".
var latencyBoundsMs = [...]float64{1, 2, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000, 30000}

const latencyBuckets = len(latencyBoundsMs) + 1

// routeMetrics counts one name's requests.
type routeMetrics struct {
	requests, s2xx, s3xx, s4xx, s5xx, errors, bytes atomic.Uint64
	latency                                         [latencyBuckets]atomic.Uint64
}

// proxyMetrics is one proxy's counters since it started.
type proxyMetrics struct {
	started time.Time
	routes  sync.Map     // name → *routeMetrics, at most maxRouteKeys names
	keys    atomic.Int64 // names in routes
	active  atomic.Int64 // open client connections
	busy    atomic.Int64 // of those, new (no request read yet) or mid-request: draining waits for them
	states  sync.Map     // net.Conn → http.ConnState, to move busy on each change
	certs   atomic.Uint64
}

func newProxyMetrics() *proxyMetrics { return &proxyMetrics{started: time.Now()} }

func bucketFor(d time.Duration) int {
	ms := float64(d) / float64(time.Millisecond)
	for i, b := range latencyBoundsMs {
		if ms <= b {
			return i
		}
	}
	return latencyBuckets - 1
}

// record counts one response: its status, time to first byte, and bytes written.
// An upstream that failed (no response) is status 502 with failed set.
// A request that matched no registered name counts under unknownRoute (the caller
// passes ""), so a Host header can never add names; past maxRouteKeys names the
// rest count under otherRoutes.
func (m *proxyMetrics) record(name string, status int, ttfb time.Duration, bytes int64, failed bool) {
	if name == "" {
		name = unknownRoute
	}
	v, ok := m.routes.Load(name)
	if !ok {
		if m.keys.Add(1) > maxRouteKeys {
			m.keys.Add(-1)
			name = otherRoutes
		}
		var loaded bool
		v, loaded = m.routes.LoadOrStore(name, &routeMetrics{})
		if loaded && name != otherRoutes {
			m.keys.Add(-1) // another request stored it first
		}
	}
	r := v.(*routeMetrics)
	r.requests.Add(1)
	switch {
	case status >= 500:
		r.s5xx.Add(1)
	case status >= 400:
		r.s4xx.Add(1)
	case status >= 300:
		r.s3xx.Add(1)
	default:
		r.s2xx.Add(1)
	}
	if failed {
		r.errors.Add(1)
	}
	if bytes > 0 {
		r.bytes.Add(uint64(bytes))
	}
	r.latency[bucketFor(ttfb)].Add(1)
}

// routeSnapshot is one name's counters at a moment (cumulative, or a window's delta).
type routeSnapshot struct {
	Requests uint64                 `json:"requests"`
	S2xx     uint64                 `json:"s2xx,omitempty"`
	S3xx     uint64                 `json:"s3xx,omitempty"`
	S4xx     uint64                 `json:"s4xx,omitempty"`
	S5xx     uint64                 `json:"s5xx,omitempty"`
	Errors   uint64                 `json:"errors,omitempty"`
	Bytes    uint64                 `json:"bytes,omitempty"`
	Latency  [latencyBuckets]uint64 `json:"latency"`
}

func (a *routeSnapshot) add(b routeSnapshot) {
	a.Requests += b.Requests
	a.S2xx += b.S2xx
	a.S3xx += b.S3xx
	a.S4xx += b.S4xx
	a.S5xx += b.S5xx
	a.Errors += b.Errors
	a.Bytes += b.Bytes
	for i := range a.Latency {
		a.Latency[i] += b.Latency[i]
	}
}

func (a routeSnapshot) minus(b routeSnapshot) routeSnapshot {
	d := routeSnapshot{Requests: a.Requests - b.Requests, S2xx: a.S2xx - b.S2xx, S3xx: a.S3xx - b.S3xx,
		S4xx: a.S4xx - b.S4xx, S5xx: a.S5xx - b.S5xx, Errors: a.Errors - b.Errors, Bytes: a.Bytes - b.Bytes}
	for i := range d.Latency {
		d.Latency[i] = a.Latency[i] - b.Latency[i]
	}
	return d
}

// percentile is the bucket bound under which fraction q of the requests fell.
func (a routeSnapshot) percentile(q float64) time.Duration {
	var total uint64
	for _, n := range a.Latency {
		total += n
	}
	if total == 0 {
		return 0
	}
	want := uint64(q*float64(total) + 0.5)
	var seen uint64
	for i, n := range a.Latency {
		seen += n
		if seen >= want && n > 0 {
			if i >= len(latencyBoundsMs) {
				return time.Duration(latencyBoundsMs[len(latencyBoundsMs)-1]) * time.Millisecond * 2
			}
			return time.Duration(latencyBoundsMs[i] * float64(time.Millisecond))
		}
	}
	return 0
}

// processStats is the proxy process at a moment.
type processStats struct {
	PID        int     `json:"pid"`
	Version    string  `json:"version"`
	Uptime     float64 `json:"uptimeSeconds"`
	MemoryMB   float64 `json:"memoryMB"`
	Goroutines int     `json:"goroutines"`
	CPUSeconds float64 `json:"cpuSeconds"`
	Conns      int64   `json:"openConnections"`
	Certs      uint64  `json:"certificatesIssued"`
}

// metricsSnapshot is what `tsp stats` reads live.
type metricsSnapshot struct {
	Process processStats             `json:"process"`
	Routes  map[string]routeSnapshot `json:"routes"`
}

func (m *proxyMetrics) snapshot() metricsSnapshot {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	s := metricsSnapshot{
		Process: processStats{
			PID: os.Getpid(), Version: Version, Uptime: time.Since(m.started).Seconds(),
			MemoryMB: float64(ms.Sys) / (1 << 20), Goroutines: runtime.NumGoroutine(),
			CPUSeconds: cpuSeconds(), Conns: m.active.Load(), Certs: m.certs.Load(),
		},
		Routes: map[string]routeSnapshot{},
	}
	m.routes.Range(func(k, v any) bool {
		r := v.(*routeMetrics)
		rs := routeSnapshot{Requests: r.requests.Load(), S2xx: r.s2xx.Load(), S3xx: r.s3xx.Load(), S4xx: r.s4xx.Load(),
			S5xx: r.s5xx.Load(), Errors: r.errors.Load(), Bytes: r.bytes.Load()}
		for i := range rs.Latency {
			rs.Latency[i] = r.latency[i].Load()
		}
		s.Routes[k.(string)] = rs
		return true
	})
	return s
}

// connState counts open client connections, and the busy ones (http.Server.ConnState:
// once per connection state change, not per request).
func (m *proxyMetrics) connState(c net.Conn, st http.ConnState) {
	isBusy := func(s http.ConnState) bool { return s == http.StateNew || s == http.StateActive }
	prev, had := m.states.Load(c)
	if had && isBusy(prev.(http.ConnState)) {
		m.busy.Add(-1)
	}
	switch st {
	case http.StateClosed, http.StateHijacked:
		m.states.Delete(c)
		m.active.Add(-1)
		return
	case http.StateNew:
		m.active.Add(1)
	}
	m.states.Store(c, st)
	if isBusy(st) {
		m.busy.Add(1)
	}
}

// meteredWriter records status, time to first byte and bytes for one response.
// Unwrap lets the reverse proxy's http.ResponseController reach the real writer
// (flushing streams, hijacking WebSocket upgrades).
type meteredWriter struct {
	http.ResponseWriter
	start  time.Time
	status int
	ttfb   time.Duration
	bytes  int64
	failed bool // the upstream did not answer (the 502 is the proxy's own)
}

func newMeteredWriter(w http.ResponseWriter) *meteredWriter {
	return &meteredWriter{ResponseWriter: w, start: time.Now()}
}

func (w *meteredWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status, w.ttfb = code, time.Since(w.start)
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *meteredWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status, w.ttfb = http.StatusOK, time.Since(w.start)
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

func (w *meteredWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// result is the response's status (200 when the handler wrote nothing) and TTFB.
func (w *meteredWriter) result() (int, time.Duration) {
	if w.status == 0 {
		return http.StatusOK, time.Since(w.start)
	}
	return w.status, w.ttfb
}

// adminSocketPath is where proxy pid answers `tsp stats`: ~/.tsp/run/<pid>.sock,
// one socket per process, so a proxy being swapped out never answers for the new
// one. A unix socket path must fit in about 100 bytes (macOS); a home deeper than
// that gets no admin socket, and tsp stats then shows the history only.
func adminSocketPath(dir string, pid int) (string, error) {
	p := filepath.Join(dir, "run", strconv.Itoa(pid)+".sock")
	if len(p) >= 100 {
		return "", fmt.Errorf("%s is too long for a unix socket", p)
	}
	return p, nil
}

// privateRunDir makes ~/.tsp/run (0700) and checks it is a real folder this user
// owns that no one else can write, so no other user can plant or swap a socket in it.
func privateRunDir(dir string) error {
	run := filepath.Join(dir, "run")
	if err := os.MkdirAll(run, 0o700); err != nil {
		return err
	}
	fi, err := os.Lstat(run)
	if err != nil {
		return err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is not a folder", run)
	}
	if !ownedByMe(fi) {
		return fmt.Errorf("%s belongs to another user", run)
	}
	if fi.Mode().Perm() != 0o700 {
		return os.Chmod(run, 0o700)
	}
	return nil
}

// adminServer answers /health and /stats (metrics() at the time of asking) on a
// unix socket until ctx ends.
func adminServer(ctx context.Context, dir string, metrics func() *proxyMetrics) {
	path, err := adminSocketPath(dir, os.Getpid())
	if err == nil {
		err = privateRunDir(dir)
	}
	if err != nil {
		appLog().Warn("no admin socket", "err", err)
		return
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		appLog().Warn("admin socket", "path", path, "err", err)
		return
	}
	_ = os.Chmod(path, 0o600)
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok " + Version + "\n")) })
	mux.HandleFunc("/stats", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(metrics().snapshot())
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
		_ = os.Remove(path)
	}()
	_ = srv.Serve(ln)
}

// adminClient talks HTTP to proxy pid's admin socket (only in a private run folder).
func adminClient(dir string, pid int, timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			path, err := adminSocketPath(dir, pid)
			if err == nil {
				err = privateRunDir(dir)
			}
			if err != nil {
				return nil, err
			}
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		},
	}}
}

// fetchLiveStats asks proxy pid for its snapshot.
func fetchLiveStats(dir string, pid int) (metricsSnapshot, error) {
	var s metricsSnapshot
	resp, err := adminClient(dir, pid, 3*time.Second).Get("http://tsp/stats")
	if err != nil {
		return s, err
	}
	defer resp.Body.Close()
	return s, json.NewDecoder(resp.Body).Decode(&s)
}

// historyLine is one minute of one proxy in stats.jsonl.
type historyLine struct {
	Time    time.Time                `json:"time"`
	Proxy   string                   `json:"proxy"` // "local" (*.localhost) or "start" (Tailscale)
	PID     int                      `json:"pid"`
	Version string                   `json:"version"`
	Memory  float64                  `json:"memoryMB"`
	Conns   int64                    `json:"openConnections"`
	Routes  map[string]routeSnapshot `json:"routes,omitempty"`
}

// historyEvery is how often a proxy appends its last minute; a quiet proxy writes
// a heartbeat line only every historyQuiet, so an idle machine writes little.
var (
	historyEvery = time.Minute
	historyQuiet = 10 * time.Minute
)

func statsHistoryPath(dir string) string { return filepath.Join(dir, "logs", "stats.jsonl") }

// historyLoop appends this proxy's per-minute deltas to stats.jsonl until ctx ends.
func historyLoop(ctx context.Context, dir, proxy string, m *proxyMetrics) {
	w := newRotatingFile(statsHistoryPath(dir), 2<<20, 3)
	defer w.Close()
	prev := metricsSnapshot{Routes: map[string]routeSnapshot{}} // from zero: requests before the first tick count too
	lastWrite := time.Now()
	t := time.NewTicker(historyEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cur := m.snapshot()
		line := historyLine{Time: time.Now().UTC(), Proxy: proxy, PID: cur.Process.PID, Version: Version,
			Memory: cur.Process.MemoryMB, Conns: cur.Process.Conns, Routes: map[string]routeSnapshot{}}
		for name, r := range cur.Routes {
			if d := r.minus(prev.Routes[name]); d.Requests > 0 {
				line.Routes[name] = d
			}
		}
		prev = cur
		if len(line.Routes) == 0 && time.Since(lastWrite) < historyQuiet {
			continue
		}
		data, err := json.Marshal(line)
		if err == nil {
			_, _ = w.Write(append(data, '\n'))
			lastWrite = time.Now()
		}
	}
}

// readHistory returns the stats.jsonl lines (rotated files included) since t.
func readHistory(dir string, since time.Time) []historyLine {
	var out []historyLine
	for _, p := range rotatedPaths(statsHistoryPath(dir), 3) {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, raw := range splitLines(data) {
			var l historyLine
			if json.Unmarshal(raw, &l) == nil && !l.Time.Before(since) {
				out = append(out, l)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out
}

func splitLines(data []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			if i > start {
				out = append(out, data[start:i])
			}
			start = i + 1
		}
	}
	if start < len(data) {
		out = append(out, data[start:])
	}
	return out
}

// slowRequest is the time to first byte above which a request is logged.
const slowRequest = 2 * time.Second

// logRequestEvent logs one request per mode (the localRequestLog setting):
// "errors" (the default) logs failed upstreams, 5xx answers and slow ones; "all"
// logs every request; "off" none.
// The path is logged escaped (%XX), as a request can carry any bytes in it.
func logRequestEvent(mode, name string, r *http.Request, status int, ttfb time.Duration, failed bool) {
	notable := failed || status >= 500 || ttfb > slowRequest
	if mode == "off" || (mode != "all" && !notable) {
		return
	}
	level := slog.LevelInfo
	if notable {
		level = slog.LevelWarn
	}
	appLog().Log(r.Context(), level, "request", "name", name, "method", r.Method, "path", r.URL.EscapedPath(),
		"status", status, "ms", ttfb.Milliseconds(), "upstreamFailed", failed)
}
