# portless-tailscale-proxy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A tiny Go CLI that polls portless's `routes.json`, runs a path-routing reverse proxy, and points a single Tailscale Funnel at it so every local dev server is reachable at `https://<node>.ts.net/<hostname>.local/...`.

**Architecture:** One Go module, standard library only. A `RouteStore` holds a `hostname→port` map refreshed from `~/.portless/routes.json` on a ticker. A `net/http` server uses `httputil.ReverseProxy` to forward by first path segment (stripped) to `127.0.0.1:port`, with Host rewrite, streaming, and WebSocket upgrades handled by the stdlib. A `funnel` module shells out to `tailscale`. A `doctor` module preflight-checks tailscale/Funnel/portless and prints install + config links. Distributed via an npm `optionalDependencies` launcher (for `npx`), GitHub Releases, Homebrew, `curl|sh`, and `go install`.

**Tech Stack:** Go (stdlib: `net/http/httputil`, `encoding/json`, `os/exec`, `flag`, `sync`, `context`); goreleaser + GitHub Actions; a small Node launcher for npm.

**Module path:** `github.com/meabed/portless-tailscale-proxy`

---

## File structure

| File | Responsibility |
| --- | --- |
| `go.mod` | module declaration (zero `require`s) |
| `main.go` | entry point, version var, command dispatch |
| `cli.go` | flag parsing per subcommand, help text, signal handling, start orchestration |
| `routes.go` | `Route`, `loadRoutes`, `defaultStatePath`, `RouteStore` |
| `proxy.go` | `splitFirstSegment`, `writeIndex`, `newHandler` (ReverseProxy) |
| `poll.go` | `poll(ctx, store, interval)` ticker loop |
| `funnel.go` | `Runner` interface, `execRunner`, `funnelStart/Reset/Status` |
| `doctor.go` | `Check`, `runDoctor`, `printChecks` |
| `detach_unix.go` / `detach_windows.go` | build-tagged `detachSysProcAttr()` + `spawnDetached` for `--bg` |
| `routes_test.go` `proxy_test.go` `funnel_test.go` `doctor_test.go` | tests |
| `npm/portless-tailscale-proxy/` | main npm package: `package.json` + `bin/launcher.js` |
| `npm/build-platform-packages.mjs` | generates per-platform npm packages from built binaries |
| `.goreleaser.yaml` | cross-compile matrix, archives, Homebrew tap, checksums |
| `.github/workflows/release.yml` | run goreleaser + publish npm packages on tag |
| `install.sh` | `curl \| sh` installer |
| `README.md` | usage + install docs |

---

## Task 1: Toolchain, module scaffold, first build

**Files:**
- Create: `go.mod`, `main.go`, `.gitignore`

- [ ] **Step 1: Install Go (if absent)**

Run:
```bash
go version 2>/dev/null || brew install go
go version
```
Expected: prints `go version go1.2x ...` (1.22+).

- [ ] **Step 2: Initialize the module**

Run:
```bash
cd /Users/meabed/workspace/meabed/portless-tailscale-proxy
go mod init github.com/meabed/portless-tailscale-proxy
```
Expected: creates `go.mod` containing `module github.com/meabed/portless-tailscale-proxy` and a `go 1.2x` line.

- [ ] **Step 3: Create `.gitignore`**

```gitignore
/dist/
/ptp
/ptp.exe
*.log
node_modules/
npm/*/bin/
```

- [ ] **Step 4: Create a minimal `main.go`**

```go
package main

import (
	"fmt"
	"os"
)

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:]))
}

// run dispatches the subcommand and returns a process exit code.
func run(argv []string) int {
	if len(argv) == 0 {
		printHelp()
		return 1
	}
	switch argv[0] {
	case "-v", "--version", "version":
		fmt.Println(version)
		return 0
	case "-h", "--help", "help":
		printHelp()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", argv[0])
		printHelp()
		return 1
	}
}

// printHelp is defined in cli.go.
```

- [ ] **Step 5: Create a temporary `printHelp` so it builds**

Add to `main.go` for now (moved to `cli.go` in Task 10):
```go
func printHelp() {
	fmt.Println("portless-tailscale-proxy (ptp) — see `ptp doctor`")
}
```

- [ ] **Step 6: Build and run**

Run:
```bash
go build -o ptp . && ./ptp --version
```
Expected: prints `dev`.

- [ ] **Step 7: Commit**

```bash
git add go.mod main.go .gitignore
git commit -m "feat: Go module scaffold and version command"
```

---

## Task 2: Route loading

**Files:**
- Create: `routes.go`, `routes_test.go`
- Test fixture: inline temp files

- [ ] **Step 1: Write the failing test**

`routes_test.go`:
```go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "routes.json")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadRoutes_parsesHostnamePort(t *testing.T) {
	p := writeTemp(t, `[
	  {"hostname":"www-web-help-ai.local","port":4764,"pid":4154},
	  {"hostname":"module-help-ai-agent-api.local","port":4434,"pid":4315}
	]`)
	m, err := loadRoutes(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := m["www-web-help-ai.local"]; got != 4764 {
		t.Errorf("want 4764, got %d", got)
	}
	if got := m["module-help-ai-agent-api.local"]; got != 4434 {
		t.Errorf("want 4434, got %d", got)
	}
}

func TestLoadRoutes_missingFileIsEmpty(t *testing.T) {
	m, err := loadRoutes(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if len(m) != 0 {
		t.Errorf("want empty map, got %d entries", len(m))
	}
}

func TestLoadRoutes_skipsInvalidEntries(t *testing.T) {
	p := writeTemp(t, `[{"hostname":"","port":1},{"hostname":"ok.local","port":0},{"hostname":"good.local","port":3000}]`)
	m, err := loadRoutes(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 || m["good.local"] != 3000 {
		t.Errorf("want only good.local=3000, got %v", m)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./... -run TestLoadRoutes`
Expected: FAIL — `undefined: loadRoutes`.

- [ ] **Step 3: Implement `routes.go` (loadRoutes + defaultStatePath)**

```go
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Route mirrors one entry in ~/.portless/routes.json.
type Route struct {
	Hostname string `json:"hostname"`
	Port     int    `json:"port"`
	PID      int    `json:"pid"`
}

// defaultStatePath returns the portless routes file under the user's home dir.
func defaultStatePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".portless", "routes.json"), nil
}

// loadRoutes reads and parses the portless routes file into a hostname→port map.
// A missing file yields an empty map and no error (portless may not be running).
func loadRoutes(statePath string) (map[string]int, error) {
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]int{}, nil
		}
		return nil, err
	}
	var routes []Route
	if err := json.Unmarshal(data, &routes); err != nil {
		return nil, err
	}
	m := make(map[string]int, len(routes))
	for _, r := range routes {
		if r.Hostname != "" && r.Port > 0 {
			m[r.Hostname] = r.Port
		}
	}
	return m, nil
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./... -run TestLoadRoutes -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add routes.go routes_test.go
git commit -m "feat: load portless routes.json into hostname→port map"
```

---

## Task 3: RouteStore (thread-safe map + refresh diff)

**Files:**
- Modify: `routes.go`
- Modify: `routes_test.go`

- [ ] **Step 1: Write the failing test**

Append to `routes_test.go`:
```go
import "sort" // add to the import block at top

func TestRouteStore_refreshDiff(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "routes.json")
	write := func(s string) {
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`[{"hostname":"a.local","port":1}]`)
	s := NewRouteStore(p)

	added, removed, err := s.refresh()
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || added[0] != "a.local" || len(removed) != 0 {
		t.Fatalf("first refresh: added=%v removed=%v", added, removed)
	}
	if port, ok := s.lookup("a.local"); !ok || port != 1 {
		t.Fatalf("lookup a.local: %d %v", port, ok)
	}

	write(`[{"hostname":"b.local","port":2}]`)
	added, removed, _ = s.refresh()
	sort.Strings(added)
	if len(added) != 1 || added[0] != "b.local" || len(removed) != 1 || removed[0] != "a.local" {
		t.Fatalf("second refresh: added=%v removed=%v", added, removed)
	}
	if _, ok := s.lookup("a.local"); ok {
		t.Fatal("a.local should be gone")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./... -run TestRouteStore`
Expected: FAIL — `undefined: NewRouteStore`.

- [ ] **Step 3: Implement RouteStore in `routes.go`**

Add imports `"sort"` and `"sync"` to `routes.go`, then append:
```go
// RouteStore holds the current hostname→port map behind a RWMutex.
type RouteStore struct {
	mu        sync.RWMutex
	routes    map[string]int
	statePath string
}

// NewRouteStore creates an empty store bound to a routes.json path.
func NewRouteStore(statePath string) *RouteStore {
	return &RouteStore{routes: map[string]int{}, statePath: statePath}
}

// lookup returns the port for a hostname and whether it is registered.
func (s *RouteStore) lookup(hostname string) (int, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.routes[hostname]
	return p, ok
}

// snapshot returns a copy of the current map.
func (s *RouteStore) snapshot() map[string]int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]int, len(s.routes))
	for k, v := range s.routes {
		out[k] = v
	}
	return out
}

// refresh reloads routes from disk, swaps the map atomically, and reports diffs.
func (s *RouteStore) refresh() (added, removed []string, err error) {
	next, err := loadRoutes(s.statePath)
	if err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	prev := s.routes
	s.routes = next
	s.mu.Unlock()
	for h := range next {
		if _, ok := prev[h]; !ok {
			added = append(added, h)
		}
	}
	for h := range prev {
		if _, ok := next[h]; !ok {
			removed = append(removed, h)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed, nil
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./... -run TestRouteStore -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add routes.go routes_test.go
git commit -m "feat: thread-safe RouteStore with refresh diffing"
```

---

## Task 4: Path segment split + index page

**Files:**
- Create: `proxy.go`
- Create: `proxy_test.go`

- [ ] **Step 1: Write the failing test**

`proxy_test.go`:
```go
package main

import "testing"

func TestSplitFirstSegment(t *testing.T) {
	cases := []struct {
		in, seg, rest string
	}{
		{"/module-help-ai-agent-api.local/foo", "module-help-ai-agent-api.local", "/foo"},
		{"/module-help-ai-agent-api.local/foo/bar", "module-help-ai-agent-api.local", "/foo/bar"},
		{"/module-help-ai-agent-api.local", "module-help-ai-agent-api.local", "/"},
		{"/module-help-ai-agent-api.local/", "module-help-ai-agent-api.local", "/"},
		{"/", "", "/"},
		{"", "", "/"},
	}
	for _, c := range cases {
		seg, rest := splitFirstSegment(c.in)
		if seg != c.seg || rest != c.rest {
			t.Errorf("splitFirstSegment(%q) = (%q,%q), want (%q,%q)", c.in, seg, rest, c.seg, c.rest)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./... -run TestSplitFirstSegment`
Expected: FAIL — `undefined: splitFirstSegment`.

- [ ] **Step 3: Implement `proxy.go` (split + index only for now)**

```go
package main

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// splitFirstSegment splits "/seg/rest..." into ("seg", "/rest...").
// "/seg" and "/seg/" both yield ("seg", "/"); "/" and "" yield ("", "/").
func splitFirstSegment(p string) (seg, rest string) {
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return "", "/"
	}
	if i := strings.IndexByte(p, '/'); i >= 0 {
		return p[:i], "/" + p[i+1:]
	}
	return p, "/"
}

// writeIndex writes a plain-text list of registered services with the given status.
func writeIndex(w http.ResponseWriter, store *RouteStore, status int) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	snap := store.snapshot()
	hosts := make([]string, 0, len(snap))
	for h := range snap {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	fmt.Fprintln(w, "portless-tailscale-proxy — registered services:")
	if len(hosts) == 0 {
		fmt.Fprintln(w, "  (none — is `portless` running? try `ptp doctor`)")
		return
	}
	for _, h := range hosts {
		fmt.Fprintf(w, "  /%s/  ->  127.0.0.1:%d\n", h, snap[h])
	}
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./... -run TestSplitFirstSegment -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add proxy.go proxy_test.go
git commit -m "feat: path segment split and index page helpers"
```

---

## Task 5: HTTP reverse-proxy handler (routing, strip, host rewrite, 404, 502)

**Files:**
- Modify: `proxy.go`
- Modify: `proxy_test.go`

- [ ] **Step 1: Write the failing test**

Append to `proxy_test.go`:
```go
import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandler_routesAndStripsPrefix(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo the path the backend actually received and the Host header.
		w.Header().Set("X-Echo-Path", r.URL.Path)
		w.Header().Set("X-Echo-Host", r.Host)
		w.Header().Set("X-Echo-Query", r.URL.RawQuery)
		io.WriteString(w, "ok")
	}))
	defer backend.Close()

	port := mustPort(t, backend.URL) // helper below
	store := NewRouteStore("")
	store.routes = map[string]int{"svc.local": port}

	h := newHandler(store)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/svc.local/foo?x=1", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Result().Header.Get("X-Echo-Path"); got != "/foo" {
		t.Errorf("backend path = %q, want /foo", got)
	}
	if got := rec.Result().Header.Get("X-Echo-Query"); got != "x=1" {
		t.Errorf("backend query = %q, want x=1", got)
	}
	if got := rec.Result().Header.Get("X-Echo-Host"); got == "" {
		t.Error("backend Host header empty; expected rewrite to 127.0.0.1:port")
	}
}

func TestHandler_unknownHostReturns404Index(t *testing.T) {
	store := NewRouteStore("")
	store.routes = map[string]int{"known.local": 4000}
	h := newHandler(store)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/nope.local/x", nil))
	if rec.Code != 404 {
		t.Fatalf("status = %d", rec.Code)
	}
	if body := rec.Body.String(); !contains(body, "known.local") {
		t.Errorf("404 body should list known services, got: %s", body)
	}
}

func TestHandler_deadBackendReturns502(t *testing.T) {
	store := NewRouteStore("")
	store.routes = map[string]int{"dead.local": 1} // nothing listens on :1
	h := newHandler(store)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/dead.local/x", nil))
	if rec.Code != 502 {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

// helpers
func mustPort(t *testing.T, rawURL string) int {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	p, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
```

Add these imports at the top of `proxy_test.go`: `"net/url"`, `"strconv"`, `"strings"`.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./... -run TestHandler`
Expected: FAIL — `undefined: newHandler`.

- [ ] **Step 3: Implement `newHandler` in `proxy.go`**

Add imports `"context"`, `"net/http/httputil"`, `"strconv"` to `proxy.go`, then append:
```go
type ctxKey int

const targetKey ctxKey = 0

// target is the resolved upstream for a single request.
type target struct {
	host string // "127.0.0.1:<port>"
	path string // rewritten path with the matched segment stripped
}

// newHandler returns an HTTP handler that routes by first path segment.
func newHandler(store *RouteStore) http.Handler {
	proxy := &httputil.ReverseProxy{
		FlushInterval: -1, // flush immediately: SSE / streaming / chunked
		Rewrite: func(pr *httputil.ProxyRequest) {
			tgt := pr.In.Context().Value(targetKey).(target)
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = tgt.host
			pr.Out.URL.Path = tgt.path
			pr.Out.URL.RawQuery = pr.In.URL.RawQuery
			pr.Out.Host = tgt.host
			pr.SetXForwarded()
			pr.Out.Header.Set("X-Forwarded-Proto", "https")
			if pr.In.Host != "" {
				pr.Out.Header.Set("X-Forwarded-Host", pr.In.Host)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprintf(w, "portless-tailscale-proxy: upstream error: %v\n", err)
		},
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seg, rest := splitFirstSegment(r.URL.Path)
		if seg == "" {
			writeIndex(w, store, http.StatusNotFound)
			return
		}
		port, ok := store.lookup(seg)
		if !ok {
			writeIndex(w, store, http.StatusNotFound)
			return
		}
		tgt := target{host: "127.0.0.1:" + strconv.Itoa(port), path: rest}
		ctx := context.WithValue(r.Context(), targetKey, tgt)
		proxy.ServeHTTP(w, r.WithContext(ctx))
	})
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./... -run TestHandler -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add proxy.go proxy_test.go
git commit -m "feat: reverse-proxy handler with prefix strip, host rewrite, 404 and 502"
```

---

## Task 6: WebSocket proxying

`httputil.ReverseProxy` upgrades automatically; this task adds a test that proves it end-to-end against a real WebSocket-ish upgrade using raw HTTP hijack (no third-party deps).

**Files:**
- Modify: `proxy_test.go`

- [ ] **Step 1: Write the failing test (raw upgrade echo)**

Append to `proxy_test.go`:
```go
import (
	"bufio"
	"net"
	"net/http"
)

// TestHandler_proxiesUpgrade verifies Connection: Upgrade is passed through and
// bytes are relayed bidirectionally. We use a minimal raw upgrade, not full WS,
// because ReverseProxy's switch-protocols path is protocol-agnostic.
func TestHandler_proxiesUpgrade(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "echo" {
			http.Error(w, "no upgrade", 400)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("backend cannot hijack")
		}
		conn, brw, err := hj.Hijack()
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: echo\r\nConnection: Upgrade\r\n\r\n")
		brw.Flush()
		// Echo one line.
		line, _ := brw.ReadString('\n')
		brw.WriteString("echo:" + line)
		brw.Flush()
	}))
	defer backend.Close()

	port := mustPort(t, backend.URL)
	store := NewRouteStore("")
	store.routes = map[string]int{"ws.local": port}

	front := httptest.NewServer(newHandler(store))
	defer front.Close()

	u, _ := url.Parse(front.URL)
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "GET /ws.local/ HTTP/1.1\r\nHost: x\r\nUpgrade: echo\r\nConnection: Upgrade\r\n\r\n")
	br := bufio.NewReader(conn)
	status, _ := br.ReadString('\n')
	if !strings.Contains(status, "101") {
		t.Fatalf("expected 101, got %q", status)
	}
	// Drain headers.
	for {
		line, _ := br.ReadString('\n')
		if line == "\r\n" || line == "" {
			break
		}
	}
	fmt.Fprintf(conn, "hello\n")
	got, _ := br.ReadString('\n')
	if !strings.Contains(got, "echo:hello") {
		t.Fatalf("relay failed, got %q", got)
	}
}
```

- [ ] **Step 2: Run to verify it passes immediately (stdlib already handles upgrades)**

Run: `go test ./... -run TestHandler_proxiesUpgrade -v`
Expected: PASS. (If it fails, confirm Go ≥1.20 so `Rewrite`/upgrade handling is present.)

- [ ] **Step 3: Commit**

```bash
git add proxy_test.go
git commit -m "test: prove WebSocket/upgrade passthrough via ReverseProxy"
```

---

## Task 7: Poller

**Files:**
- Create: `poll.go`
- Modify: `proxy_test.go` (or new `poll_test.go`)

- [ ] **Step 1: Write the failing test**

Create `poll_test.go`:
```go
package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPoll_picksUpChanges(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "routes.json")
	os.WriteFile(p, []byte(`[]`), 0o644)
	store := NewRouteStore(p)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go poll(ctx, store, 10*time.Millisecond)

	os.WriteFile(p, []byte(`[{"hostname":"x.local","port":9}]`), 0o644)

	deadline := time.After(2 * time.Second)
	for {
		if port, ok := store.lookup("x.local"); ok && port == 9 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("poll did not pick up the new route")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./... -run TestPoll`
Expected: FAIL — `undefined: poll`.

- [ ] **Step 3: Implement `poll.go`**

```go
package main

import (
	"context"
	"log"
	"time"
)

// poll refreshes the store on an interval until ctx is cancelled, doing one
// immediate refresh first. Added/removed routes are logged.
func poll(ctx context.Context, store *RouteStore, interval time.Duration) {
	refresh := func() {
		added, removed, err := store.refresh()
		if err != nil {
			log.Printf("warn: reading routes failed: %v", err)
			return
		}
		for _, h := range added {
			log.Printf("route + %s", h)
		}
		for _, h := range removed {
			log.Printf("route - %s", h)
		}
	}
	refresh()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			refresh()
		}
	}
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./... -run TestPoll -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add poll.go poll_test.go
git commit -m "feat: ticker poller that refreshes routes and logs diffs"
```

---

## Task 8: Funnel manager + Runner abstraction

**Files:**
- Create: `funnel.go`, `funnel_test.go`

- [ ] **Step 1: Write the failing test**

`funnel_test.go`:
```go
package main

import (
	"strings"
	"testing"
)

// fakeRunner records invocations and returns canned output.
type fakeRunner struct {
	calls   [][]string
	stdout  string
	stderr  string
	err     error
}

func (f *fakeRunner) Run(name string, args ...string) (string, string, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	return f.stdout, f.stderr, f.err
}

func TestFunnelStart_defaultPort(t *testing.T) {
	r := &fakeRunner{}
	if err := funnelStart(r, 8443, 443); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(r.calls[0], " ")
	if got != "tailscale funnel --bg 8443" {
		t.Fatalf("got %q", got)
	}
}

func TestFunnelStart_customPublicPort(t *testing.T) {
	r := &fakeRunner{}
	if err := funnelStart(r, 8443, 8443); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(r.calls[0], " ")
	if got != "tailscale funnel --bg --https 8443 8443" {
		t.Fatalf("got %q", got)
	}
}

func TestFunnelReset(t *testing.T) {
	r := &fakeRunner{}
	if err := funnelReset(r); err != nil {
		t.Fatal(err)
	}
	if strings.Join(r.calls[0], " ") != "tailscale funnel reset" {
		t.Fatalf("got %v", r.calls[0])
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./... -run TestFunnel`
Expected: FAIL — `undefined: funnelStart`.

- [ ] **Step 3: Implement `funnel.go`**

```go
package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
)

// Runner runs external commands. Abstracted so tests can fake `tailscale`.
type Runner interface {
	Run(name string, args ...string) (stdout, stderr string, err error)
}

type execRunner struct{}

func (execRunner) Run(name string, args ...string) (string, string, error) {
	cmd := exec.Command(name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	return out.String(), errb.String(), err
}

// funnelArgs builds the `tailscale funnel` argument list for a local proxy port.
func funnelArgs(proxyPort, publicPort int) []string {
	args := []string{"funnel", "--bg"}
	if publicPort != 443 {
		args = append(args, "--https", strconv.Itoa(publicPort))
	}
	return append(args, strconv.Itoa(proxyPort))
}

// funnelStart registers the Tailscale Funnel pointing at the local proxy port.
func funnelStart(r Runner, proxyPort, publicPort int) error {
	_, stderr, err := r.Run("tailscale", funnelArgs(proxyPort, publicPort)...)
	if err != nil {
		return fmt.Errorf("tailscale funnel failed: %v\n%s", err, stderr)
	}
	return nil
}

// funnelReset tears down the Funnel configuration.
func funnelReset(r Runner) error {
	_, stderr, err := r.Run("tailscale", "funnel", "reset")
	if err != nil {
		return fmt.Errorf("tailscale funnel reset failed: %v\n%s", err, stderr)
	}
	return nil
}

// funnelStatus returns the human-readable funnel status output.
func funnelStatus(r Runner) (string, error) {
	out, stderr, err := r.Run("tailscale", "funnel", "status")
	if err != nil {
		return "", fmt.Errorf("%v\n%s", err, stderr)
	}
	return out, nil
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./... -run TestFunnel -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add funnel.go funnel_test.go
git commit -m "feat: tailscale funnel start/reset/status with testable Runner"
```

---

## Task 9: Doctor preflight checks

**Files:**
- Create: `doctor.go`, `doctor_test.go`

- [ ] **Step 1: Write the failing test**

`doctor_test.go`:
```go
package main

import (
	"strings"
	"testing"
)

// scriptRunner returns different output per (name,args) key.
type scriptRunner struct {
	responses map[string][3]string // key -> {stdout, stderr, errMsg}
}

func (s scriptRunner) Run(name string, args ...string) (string, string, error) {
	key := name + " " + strings.Join(args, " ")
	r, ok := s.responses[key]
	if !ok {
		return "", "not stubbed", errStub
	}
	var err error
	if r[2] != "" {
		err = errString(r[2])
	}
	return r[0], r[1], err
}

func TestDoctor_tailscaleMissing(t *testing.T) {
	r := scriptRunner{responses: map[string][3]string{}} // nothing stubbed → all error
	checks := runDoctor(r, "/nonexistent/routes.json")
	c := findCheck(t, checks, "tailscale installed")
	if c.OK {
		t.Fatal("expected tailscale check to fail")
	}
	if !strings.Contains(c.Fix, "tailscale.com/download") {
		t.Errorf("fix should link to install docs, got %q", c.Fix)
	}
}

func TestDoctor_allGood(t *testing.T) {
	statePath := writeTemp(t, `[{"hostname":"a.local","port":1}]`)
	r := scriptRunner{responses: map[string][3]string{
		"tailscale version":       {"1.80.0", "", ""},
		"tailscale status":        {"100.1.1.1 node user macOS -", "", ""},
		"tailscale funnel status": {"https://node.ts.net (Funnel on)", "", ""},
	}}
	checks := runDoctor(r, statePath)
	for _, c := range checks {
		if !c.OK {
			t.Errorf("check %q unexpectedly failed: %s", c.Name, c.Detail)
		}
	}
}

func findCheck(t *testing.T, checks []Check, name string) Check {
	t.Helper()
	for _, c := range checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("check %q not found", name)
	return Check{}
}
```

Add a tiny error helper at the bottom of `doctor_test.go`:
```go
type errString string

func (e errString) Error() string { return string(e) }

var errStub = errString("stub: not configured")
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./... -run TestDoctor`
Expected: FAIL — `undefined: runDoctor`.

- [ ] **Step 3: Implement `doctor.go`**

```go
package main

import (
	"fmt"
	"os"
	"strings"
)

// Check is one preflight result with a remediation hint.
type Check struct {
	Name   string
	OK     bool
	Detail string
	Fix    string
}

const (
	linkTailscaleInstall = "https://tailscale.com/download"
	linkFunnelKB         = "https://tailscale.com/kb/1223/funnel"
	linkHTTPSKB          = "https://tailscale.com/kb/1153/enabling-https"
	linkPortless         = "https://portless.sh"
)

// runDoctor probes tailscale, Funnel, and portless and returns ordered checks.
func runDoctor(r Runner, statePath string) []Check {
	var checks []Check

	verOut, _, verErr := r.Run("tailscale", "version")
	if verErr != nil {
		checks = append(checks, Check{
			Name: "tailscale installed", OK: false,
			Detail: "`tailscale` not found on PATH",
			Fix:    "Install Tailscale: " + linkTailscaleInstall,
		})
	} else {
		checks = append(checks, Check{"tailscale installed", true, firstLine(verOut), ""})

		statusOut, _, statusErr := r.Run("tailscale", "status")
		if statusErr != nil || strings.Contains(statusOut, "Logged out") {
			checks = append(checks, Check{
				Name: "tailscale up", OK: false, Detail: "node is not logged in",
				Fix: "Run: tailscale up   (https://tailscale.com/kb/1080/cli#up)",
			})
		} else {
			checks = append(checks, Check{"tailscale up", true, "", ""})
		}

		_, fStderr, fErr := r.Run("tailscale", "funnel", "status")
		if fErr != nil {
			checks = append(checks, Check{
				Name: "funnel enabled", OK: false, Detail: strings.TrimSpace(fStderr),
				Fix: "Enable Funnel for your tailnet:\n" +
					"  - Overview: " + linkFunnelKB + "\n" +
					"  - Enable HTTPS certs: " + linkHTTPSKB + "\n" +
					"  - Grant the `funnel` node attribute in your tailnet policy file (admin console)",
			})
		} else {
			checks = append(checks, Check{"funnel enabled", true, "", ""})
		}
	}

	if _, err := os.Stat(statePath); err != nil {
		checks = append(checks, Check{
			Name: "portless routes", OK: false, Detail: statePath + " not found",
			Fix: "Install & start portless:\n" +
				"  - " + linkPortless + "\n" +
				"  - npm install -g portless\n" +
				"  - portless proxy start",
		})
	} else if m, err := loadRoutes(statePath); err != nil {
		checks = append(checks, Check{"portless routes", false, "parse error: " + err.Error(), "Inspect " + statePath})
	} else {
		checks = append(checks, Check{"portless routes", true, fmt.Sprintf("%d route(s)", len(m)), ""})
	}

	return checks
}

// firstLine returns the first line of s, trimmed.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// printChecks writes a ✓/✗ summary and returns true if every check passed.
func printChecks(checks []Check) bool {
	allOK := true
	for _, c := range checks {
		mark := "✓"
		if !c.OK {
			mark = "✗"
			allOK = false
		}
		line := fmt.Sprintf("%s %s", mark, c.Name)
		if c.Detail != "" {
			line += "  (" + c.Detail + ")"
		}
		fmt.Println(line)
		if !c.OK && c.Fix != "" {
			for _, fl := range strings.Split(c.Fix, "\n") {
				fmt.Println("    " + fl)
			}
		}
	}
	return allOK
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./... -run TestDoctor -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add doctor.go doctor_test.go
git commit -m "feat: doctor preflight checks with install/config links"
```

---

## Task 10: CLI wiring, signals, background mode

**Files:**
- Create: `cli.go`, `detach_unix.go`, `detach_windows.go`
- Modify: `main.go` (remove the temporary `printHelp`)

- [ ] **Step 1: Remove the temporary `printHelp` from `main.go`**

Delete the `func printHelp()` block added in Task 1 Step 5 (it now lives in `cli.go`). Also extend the dispatch in `run` to handle the real commands. Replace the `switch` in `main.go`'s `run` with:
```go
	switch argv[0] {
	case "start":
		return cmdStart(argv[1:])
	case "reset":
		return cmdReset(argv[1:])
	case "status":
		return cmdStatus(argv[1:])
	case "list":
		return cmdList(argv[1:])
	case "doctor":
		return cmdDoctor(argv[1:])
	case "-v", "--version", "version":
		fmt.Println(version)
		return 0
	case "-h", "--help", "help":
		printHelp()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", argv[0])
		printHelp()
		return 1
	}
```

- [ ] **Step 2: Create `cli.go`**

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func printHelp() {
	fmt.Print(`portless-tailscale-proxy (ptp) — route a Tailscale Funnel to portless dev servers

Usage:
  ptp <command> [flags]

Commands:
  start            Preflight, run the proxy, and start the Tailscale Funnel
  reset            Stop the Funnel (tailscale funnel reset) and exit
  status           Print Funnel status and the current route map
  list             Print the live hostname→port map and public URLs
  doctor           Check tailscale / Funnel / portless and print fix links

Common flags (start):
  --port <n>          Local proxy HTTP port             (default 8443)
  --interval <sec>    Route refresh period in seconds   (default 20)
  --state <path>      routes.json path                  (default ~/.portless/routes.json)
  --funnel-port <n>   Public funnel port 443|8443|10000 (default 443)
  --bg                Run ptp detached in the background
  --no-funnel         Proxy only; print the tailscale command to run manually
  -h, --help          Show help
  -v, --version       Show version
`)
}

// resolveStatePath returns the flag value or the default ~/.portless/routes.json.
func resolveStatePath(flagVal string) (string, error) {
	if flagVal != "" {
		return flagVal, nil
	}
	return defaultStatePath()
}

type startOpts struct {
	port       int
	interval   int
	state      string
	funnelPort int
	bg         bool
	noFunnel   bool
}

func cmdStart(argv []string) int {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	var o startOpts
	fs.IntVar(&o.port, "port", 8443, "local proxy HTTP port")
	fs.IntVar(&o.interval, "interval", 20, "route refresh period (seconds)")
	fs.StringVar(&o.state, "state", "", "routes.json path")
	fs.IntVar(&o.funnelPort, "funnel-port", 443, "public funnel port")
	fs.BoolVar(&o.bg, "bg", false, "run detached in background")
	var fg bool
	fs.BoolVar(&fg, "fg", false, "run in foreground (default)")
	fs.BoolVar(&o.noFunnel, "no-funnel", false, "proxy only; print funnel command")
	if err := fs.Parse(argv); err != nil {
		return 2
	}

	if o.bg {
		logPath := "ptp.log"
		pid, err := spawnDetached(logPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to detach: %v\n", err)
			return 1
		}
		fmt.Printf("portless-tailscale-proxy running in background (pid %d), logs → %s\n", pid, logPath)
		return 0
	}

	statePath, err := resolveStatePath(o.state)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot resolve state path: %v\n", err)
		return 1
	}

	runner := execRunner{}

	// Preflight (non-fatal in --no-funnel mode).
	checks := runDoctor(runner, statePath)
	allOK := printChecks(checks)
	if !allOK && !o.noFunnel {
		fmt.Fprintln(os.Stderr, "\npreflight failed — fix the items above, or use --no-funnel to run the proxy alone")
		return 1
	}

	store := NewRouteStore(statePath)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go poll(ctx, store, time.Duration(o.interval)*time.Second)

	srv := &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", o.port),
		Handler: newHandler(store),
	}

	if o.noFunnel {
		fmt.Printf("proxy only — run this to expose it publicly:\n  tailscale %s\n",
			strings.Join(funnelArgs(o.port, o.funnelPort), " "))
	} else {
		if err := funnelStart(runner, o.port, o.funnelPort); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return 1
		}
		fmt.Printf("Tailscale Funnel → 127.0.0.1:%d (public port %d)\n", o.port, o.funnelPort)
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
		if !o.noFunnel {
			if err := funnelReset(runner); err != nil {
				log.Printf("warn: %v", err)
			}
		}
	}()

	log.Printf("listening on http://127.0.0.1:%d", o.port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		return 1
	}
	return 0
}

func cmdReset(argv []string) int {
	if err := funnelReset(execRunner{}); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	fmt.Println("Tailscale Funnel reset.")
	return 0
}

func cmdStatus(argv []string) int {
	out, err := funnelStatus(execRunner{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "funnel status: %v\n", err)
	} else {
		fmt.Println("Funnel status:")
		fmt.Println(out)
	}
	return cmdList(argv)
}

func cmdList(argv []string) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	state := fs.String("state", "", "routes.json path")
	_ = fs.Parse(argv)
	statePath, err := resolveStatePath(*state)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	m, err := loadRoutes(statePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	if len(m) == 0 {
		fmt.Println("No portless routes found. Is `portless` running? Try `ptp doctor`.")
		return 0
	}
	fmt.Println("Registered services:")
	for h, p := range m {
		fmt.Printf("  /%s/  ->  127.0.0.1:%d\n", h, p)
	}
	return 0
}

func cmdDoctor(argv []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	state := fs.String("state", "", "routes.json path")
	_ = fs.Parse(argv)
	statePath, err := resolveStatePath(*state)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	if printChecks(runDoctor(execRunner{}, statePath)) {
		fmt.Println("\nAll checks passed — you're ready to `ptp start`.")
		return 0
	}
	return 1
}
```

- [ ] **Step 3: Create `detach_unix.go`**

```go
//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// spawnDetached re-execs ptp without --bg, detached, with output to logPath.
func spawnDetached(logPath string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(exe, childArgs()...)
	cmd.Stdout = f
	cmd.Stderr = f
	cmd.SysProcAttr = detachSysProcAttr()
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	return cmd.Process.Pid, nil
}

// childArgs returns os.Args[1:] with the --bg flag removed.
func childArgs() []string {
	out := make([]string, 0, len(os.Args)-1)
	for _, a := range os.Args[1:] {
		if a == "--bg" || a == "-bg" {
			continue
		}
		out = append(out, a)
	}
	return out
}
```

- [ ] **Step 4: Create `detach_windows.go`**

```go
//go:build windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

const (
	detachedProcess        = 0x00000008
	createNewProcessGroup  = 0x00000200
)

func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewProcessGroup}
}

func spawnDetached(logPath string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(exe, childArgs()...)
	cmd.Stdout = f
	cmd.Stderr = f
	cmd.SysProcAttr = detachSysProcAttr()
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	return cmd.Process.Pid, nil
}

func childArgs() []string {
	out := make([]string, 0, len(os.Args)-1)
	for _, a := range os.Args[1:] {
		if a == "--bg" || a == "-bg" {
			continue
		}
		out = append(out, a)
	}
	return out
}
```

- [ ] **Step 5: Build for both OSes and run tests**

Run:
```bash
go vet ./...
GOOS=windows GOARCH=amd64 go build -o /dev/null . 2>&1 || GOOS=windows GOARCH=amd64 go build -o nul .
go build -o ptp . && go test ./...
```
Expected: `go vet` clean; both builds succeed; all tests PASS.

- [ ] **Step 6: Smoke the CLI**

Run:
```bash
./ptp --help
./ptp doctor
./ptp list
```
Expected: help text; doctor prints ✓/✗ with links; list prints the four live portless routes (since portless is running).

- [ ] **Step 7: Commit**

```bash
git add main.go cli.go detach_unix.go detach_windows.go
git commit -m "feat: CLI commands, signal handling, and background mode"
```

---

## Task 11: Manual end-to-end verification

**Files:** none (verification only)

- [ ] **Step 1: Confirm portless routes exist**

Run: `cat ~/.portless/routes.json`
Expected: a JSON array with hostnames + ports.

- [ ] **Step 2: Start proxy only (no funnel) on a test port**

Run:
```bash
./ptp start --no-funnel --port 8799 &
sleep 1
```
Expected: prints preflight + "proxy only — run this to expose it publicly: tailscale funnel --bg 8799".

- [ ] **Step 3: Hit a real backend through the proxy**

Run (use a hostname from Step 1):
```bash
curl -s -i http://127.0.0.1:8799/module-help-ai-agent-api.local/ | head -20
curl -s -i http://127.0.0.1:8799/does-not-exist.local/ | head -5
```
Expected: first returns the dev server's response (200/redirect from `127.0.0.1:4434`); second returns `404` with the service list.

- [ ] **Step 4: Stop the test proxy**

Run: `kill %1 2>/dev/null; wait 2>/dev/null; true`

- [ ] **Step 5: (Optional) full funnel run**

Run: `./ptp start --port 8443` then visit `https://<node>.ts.net/<hostname>.local/`. Stop with Ctrl-C and confirm `tailscale funnel status` shows `No serve config` afterward.

- [ ] **Step 6: Commit any fixes discovered**

```bash
git add -A && git commit -m "fix: e2e adjustments" || echo "no changes"
```

---

## Task 12: npm optionalDependencies launcher

**Files:**
- Create: `npm/portless-tailscale-proxy/package.json`
- Create: `npm/portless-tailscale-proxy/bin/launcher.js`
- Create: `npm/build-platform-packages.mjs`
- Create: `npm/README.md` (short; can reuse root README later)

- [ ] **Step 1: Create the main package `package.json`**

`npm/portless-tailscale-proxy/package.json`:
```json
{
  "name": "portless-tailscale-proxy",
  "version": "0.0.0",
  "description": "Route a single Tailscale Funnel to all your portless dev servers by URL path.",
  "bin": {
    "portless-tailscale-proxy": "bin/launcher.js",
    "ptp": "bin/launcher.js"
  },
  "files": ["bin/launcher.js", "README.md"],
  "keywords": ["portless", "tailscale", "funnel", "proxy", "reverse-proxy", "dev"],
  "license": "MIT",
  "repository": { "type": "git", "url": "git+https://github.com/meabed/portless-tailscale-proxy.git" },
  "optionalDependencies": {
    "portless-tailscale-proxy-darwin-arm64": "0.0.0",
    "portless-tailscale-proxy-darwin-x64": "0.0.0",
    "portless-tailscale-proxy-linux-x64": "0.0.0",
    "portless-tailscale-proxy-linux-arm64": "0.0.0",
    "portless-tailscale-proxy-win32-x64": "0.0.0",
    "portless-tailscale-proxy-win32-arm64": "0.0.0"
  }
}
```

- [ ] **Step 2: Create the launcher**

`npm/portless-tailscale-proxy/bin/launcher.js`:
```js
#!/usr/bin/env node
"use strict";

const { spawnSync } = require("node:child_process");

// Map Node's platform/arch to our per-platform package + binary name.
function resolveBinary() {
  const platform = process.platform; // 'darwin' | 'linux' | 'win32'
  const arch = process.arch;         // 'x64' | 'arm64'
  const pkg = `portless-tailscale-proxy-${platform}-${arch}`;
  const exe = platform === "win32" ? "ptp.exe" : "ptp";
  try {
    return require.resolve(`${pkg}/bin/${exe}`);
  } catch {
    return null;
  }
}

const bin = resolveBinary();
if (!bin) {
  console.error(
    `portless-tailscale-proxy: no prebuilt binary for ${process.platform}-${process.arch}.\n` +
      `Install from source: go install github.com/meabed/portless-tailscale-proxy@latest\n` +
      `or download a release: https://github.com/meabed/portless-tailscale-proxy/releases`
  );
  process.exit(1);
}

const res = spawnSync(bin, process.argv.slice(2), { stdio: "inherit" });
if (res.error) {
  console.error(res.error.message);
  process.exit(1);
}
process.exit(res.status === null ? 1 : res.status);
```

- [ ] **Step 3: Create the platform-package generator**

`npm/build-platform-packages.mjs`:
```js
// Generates npm/dist/<pkg>/ for each platform from binaries in dist/.
// Expects goreleaser/CI to have produced binaries at:
//   dist/ptp_<os>_<arch>/ptp[.exe]
// Usage: node npm/build-platform-packages.mjs <version>
import { mkdirSync, writeFileSync, copyFileSync, existsSync } from "node:fs";
import { join } from "node:path";

const version = process.argv[2];
if (!version) {
  console.error("usage: node npm/build-platform-packages.mjs <version>");
  process.exit(1);
}

// [npmPlatform, npmArch, goBinDir, exe]
const targets = [
  ["darwin", "arm64", "ptp_darwin_arm64", "ptp"],
  ["darwin", "x64", "ptp_darwin_amd64_v1", "ptp"],
  ["linux", "x64", "ptp_linux_amd64_v1", "ptp"],
  ["linux", "arm64", "ptp_linux_arm64", "ptp"],
  ["win32", "x64", "ptp_windows_amd64_v1", "ptp.exe"],
  ["win32", "arm64", "ptp_windows_arm64", "ptp.exe"],
];

for (const [os, arch, goDir, exe] of targets) {
  const pkgName = `portless-tailscale-proxy-${os}-${arch}`;
  const outDir = join("npm", "dist", pkgName);
  const binDir = join(outDir, "bin");
  mkdirSync(binDir, { recursive: true });

  const src = join("dist", goDir, exe);
  if (!existsSync(src)) {
    console.error(`missing binary: ${src}`);
    process.exit(1);
  }
  copyFileSync(src, join(binDir, exe));

  const pkg = {
    name: pkgName,
    version,
    description: `Prebuilt portless-tailscale-proxy binary for ${os}-${arch}.`,
    os: [os],
    cpu: [arch],
    license: "MIT",
    repository: { type: "git", url: "git+https://github.com/meabed/portless-tailscale-proxy.git" },
    files: [`bin/${exe}`],
  };
  writeFileSync(join(outDir, "package.json"), JSON.stringify(pkg, null, 2) + "\n");
  console.log(`prepared ${pkgName}@${version}`);
}
```

- [ ] **Step 4: Validate the launcher logic locally**

Run:
```bash
node -e "const p=process.platform,a=process.arch;console.log('portless-tailscale-proxy-'+p+'-'+a)"
node --check npm/portless-tailscale-proxy/bin/launcher.js
node --check npm/build-platform-packages.mjs
```
Expected: prints the package name for this machine (e.g. `portless-tailscale-proxy-darwin-arm64`); both `--check` calls print nothing (syntax OK).

- [ ] **Step 5: Commit**

```bash
git add npm/
git commit -m "feat: npm launcher and per-platform package generator"
```

---

## Task 13: Release automation (goreleaser, GitHub Actions, Homebrew, installer)

**Files:**
- Create: `.goreleaser.yaml`, `.github/workflows/release.yml`, `install.sh`

- [ ] **Step 1: Create `.goreleaser.yaml`**

```yaml
version: 2
project_name: ptp
before:
  hooks:
    - go mod tidy
builds:
  - id: ptp
    main: .
    binary: ptp
    env: [CGO_ENABLED=0]
    ldflags:
      - -s -w -X main.version={{.Version}}
    goos: [darwin, linux, windows]
    goarch: [amd64, arm64]
archives:
  - id: ptp
    name_template: "ptp_{{ .Os }}_{{ .Arch }}"
    formats: [tar.gz]
    format_overrides:
      - goos: windows
        formats: [zip]
checksum:
  name_template: "checksums.txt"
brews:
  - name: ptp
    repository:
      owner: meabed
      name: homebrew-tap
    homepage: "https://github.com/meabed/portless-tailscale-proxy"
    description: "Route a Tailscale Funnel to portless dev servers by URL path"
    license: "MIT"
    install: |
      bin.install "ptp"
release:
  github:
    owner: meabed
    name: portless-tailscale-proxy
```

- [ ] **Step 2: Validate the goreleaser config (if goreleaser is available)**

Run:
```bash
command -v goreleaser >/dev/null && goreleaser check || echo "goreleaser not installed locally — CI will validate"
```
Expected: `config is valid` or the skip message.

- [ ] **Step 3: Create the release workflow**

`.github/workflows/release.yml`:
```yaml
name: release
on:
  push:
    tags: ["v*"]
permissions:
  contents: write
jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - uses: actions/setup-go@v5
        with: { go-version: "stable" }
      - uses: actions/setup-node@v4
        with:
          node-version: "20"
          registry-url: "https://registry.npmjs.org"
      - name: Run goreleaser
        uses: goreleaser/goreleaser-action@v6
        with:
          version: "~> v2"
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          HOMEBREW_TAP_GITHUB_TOKEN: ${{ secrets.HOMEBREW_TAP_GITHUB_TOKEN }}
      - name: Build npm platform packages
        run: node npm/build-platform-packages.mjs "${GITHUB_REF_NAME#v}"
      - name: Publish per-platform npm packages
        run: |
          for d in npm/dist/*/; do
            (cd "$d" && npm publish --access public)
          done
        env:
          NODE_AUTH_TOKEN: ${{ secrets.NPM_TOKEN }}
      - name: Publish launcher package
        run: |
          cd npm/portless-tailscale-proxy
          npm version "${GITHUB_REF_NAME#v}" --no-git-tag-version --allow-same-version
          # Pin optionalDependencies to this version
          node -e '
            const fs=require("fs");const v=process.env.GITHUB_REF_NAME.replace(/^v/,"");
            const p=JSON.parse(fs.readFileSync("package.json"));
            for(const k of Object.keys(p.optionalDependencies)) p.optionalDependencies[k]=v;
            fs.writeFileSync("package.json",JSON.stringify(p,null,2)+"\n");
          '
          npm publish --access public
        env:
          NODE_AUTH_TOKEN: ${{ secrets.NPM_TOKEN }}
```

- [ ] **Step 4: Create the `curl | sh` installer**

`install.sh`:
```sh
#!/bin/sh
# portless-tailscale-proxy installer — downloads the right release binary.
set -eu
REPO="meabed/portless-tailscale-proxy"
BIN="ptp"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) echo "unsupported arch: $arch" >&2; exit 1 ;;
esac
case "$os" in
  darwin|linux) ;;
  *) echo "unsupported OS: $os (Windows: use npm or a GitHub release)" >&2; exit 1 ;;
esac

tag=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | head -1 | cut -d'"' -f4)
[ -n "$tag" ] || { echo "could not resolve latest release tag" >&2; exit 1; }

url="https://github.com/${REPO}/releases/download/${tag}/ptp_${os}_${arch}.tar.gz"
tmp=$(mktemp -d)
echo "Downloading $url"
curl -fsSL "$url" | tar -xz -C "$tmp"
chmod +x "$tmp/$BIN"

if [ -w "$INSTALL_DIR" ]; then
  mv "$tmp/$BIN" "$INSTALL_DIR/$BIN"
else
  echo "Installing to $INSTALL_DIR (sudo)"
  sudo mv "$tmp/$BIN" "$INSTALL_DIR/$BIN"
fi
rm -rf "$tmp"
echo "Installed $BIN to $INSTALL_DIR. Run: $BIN doctor"
```

- [ ] **Step 5: Lint the shell script**

Run:
```bash
sh -n install.sh && echo "install.sh syntax OK"
```
Expected: `install.sh syntax OK`.

- [ ] **Step 6: Commit**

```bash
chmod +x install.sh
git add .goreleaser.yaml .github/workflows/release.yml install.sh
git commit -m "ci: goreleaser, release workflow, Homebrew tap, curl|sh installer"
```

---

## Task 14: README and final polish

**Files:**
- Create: `README.md`, `LICENSE`

- [ ] **Step 1: Create `LICENSE` (MIT)**

Run:
```bash
cat > LICENSE <<'EOF'
MIT License

Copyright (c) 2026 Mohamed Meabed

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
EOF
```

- [ ] **Step 2: Create `README.md`**

```markdown
# portless-tailscale-proxy (`ptp`)

Route a **single Tailscale Funnel** to **all** your [portless](https://portless.sh)
dev servers by URL path. Funnel can only expose one hostname, so `ptp` puts a tiny
path-routing reverse proxy behind it: the first path segment is the portless
hostname and selects which local server to forward to.

```
https://<node>.ts.net/module-help-ai-agent-api.local/foo
        └────────────┬───────────────────────────┘
        ptp strips the segment → 127.0.0.1:4434/foo
```

## Install

**npm / npx** (no Go needed):
```bash
npx portless-tailscale-proxy doctor
npm i -g portless-tailscale-proxy
```

**Homebrew:**
```bash
brew install meabed/tap/ptp
```

**curl | sh:**
```bash
curl -fsSL https://raw.githubusercontent.com/meabed/portless-tailscale-proxy/main/install.sh | sh
```

**Go:**
```bash
go install github.com/meabed/portless-tailscale-proxy@latest
```

## Usage

```bash
ptp doctor     # check tailscale / Funnel / portless and print fix links
ptp start      # preflight, run the proxy, start the Funnel (Ctrl-C resets it)
ptp list       # show live hostname → port map
ptp status     # Funnel status + routes
ptp reset      # tailscale funnel reset
```

Flags: `--port` (8443), `--interval` (20s), `--state` (~/.portless/routes.json),
`--funnel-port` (443|8443|10000), `--bg`, `--no-funnel`.

## Requirements

- [Tailscale](https://tailscale.com/download) with **Funnel enabled**
  ([HTTPS certs](https://tailscale.com/kb/1153/enabling-https) +
  the `funnel` node attribute in your tailnet policy —
  see [Funnel docs](https://tailscale.com/kb/1223/funnel)).
- [portless](https://portless.sh) running locally (`portless proxy start`).

Run `ptp doctor` and it will tell you exactly what's missing with links.

## Platforms

macOS, Linux, Windows, and WSL (amd64 + arm64).

## License

MIT
```

- [ ] **Step 3: Final full build + test + vet**

Run:
```bash
go vet ./... && go test ./... && go build -o ptp . && ./ptp doctor
```
Expected: vet clean, tests PASS, builds, doctor runs.

- [ ] **Step 4: Commit**

```bash
git add README.md LICENSE
git commit -m "docs: README and MIT license"
```

- [ ] **Step 5: Tag a first release (optional, when ready to publish)**

Run:
```bash
git tag v0.1.0 && git push origin master --tags
```
Expected: pushes the tag; GitHub Actions runs goreleaser + npm publish (requires `NPM_TOKEN` and `HOMEBREW_TAP_GITHUB_TOKEN` repo secrets, and a `meabed/homebrew-tap` repo).

---

## Self-review notes

- **Spec coverage:** routing/strip (Task 5), streaming+WS (Tasks 5–6), poller (Task 7),
  funnel fg/bg + reset/status (Tasks 8, 10), doctor + links (Task 9), CLI commands +
  flags + `--bg`/`--no-funnel` (Task 10), platforms via build tags + goreleaser matrix
  (Tasks 10, 13), zero runtime deps (stdlib only), 5 distribution channels (Tasks 12–14).
- **Funnel fg semantics:** `tailscale funnel` is always registered with `--bg` (foreground
  `tailscale funnel` blocks); `ptp`'s own `--bg`/`--fg` controls whether the `ptp` process
  detaches. This is the buildable interpretation of the spec's fg/bg requirement.
- **Type consistency:** `Runner.Run(name, args...) (stdout, stderr, err)`, `RouteStore`
  methods `lookup/snapshot/refresh`, `Check{Name,OK,Detail,Fix}`, `funnelArgs/Start/Reset/Status`,
  `newHandler`, `splitFirstSegment`, `writeIndex`, `spawnDetached/childArgs` are referenced
  consistently across tasks.
- **Secrets/prereqs for release:** repo secrets `NPM_TOKEN`, `HOMEBREW_TAP_GITHUB_TOKEN`,
  and a `meabed/homebrew-tap` repository (noted in Task 14 Step 5).
```
