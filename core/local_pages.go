package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// What the local HTTPS proxy answers itself: https://tsp.localhost (the
// dashboard and /api/status), a name nothing runs at (404), a name whose app does
// not answer (502), and every http://*.localhost request (308 to https). Browsers
// get HTML, every other client the same as plain text.

// dashboardName is the reserved name of the dashboard: https://tsp.localhost.
const dashboardName = "tsp"

const (
	dashboardURL = "https://" + dashboardName + "." + localTLD
	docsURL      = "https://tailscaleproxy.vercel.app/local-https"
)

// reservedNames are names `tsp run` refuses: the proxy answers them itself.
var reservedNames = map[string]string{
	dashboardName: dashboardURL + " is the dashboard of the running names",
	healthName:    "the proxy's own health check",
}

// runExample is how to start one kind of server under tsp: the shell form, so
// $PORT and $HOST expand when the command runs, not in the caller's shell.
type runExample struct {
	Stack   string `json:"stack"`
	Command string `json:"command"`        // "%s" is the name
	Note    string `json:"note,omitempty"` // why it needs no quotes: the app reads PORT itself
}

var runExamples = []runExample{
	{"Vite", `tsp run %s -- 'vite --port $PORT --host $HOST'`, ""},
	{"Next.js", `tsp run %s -- 'next dev -p $PORT -H $HOST'`, ""},
	{"FastAPI", `tsp run %s -- 'uvicorn main:app --reload --port $PORT --host $HOST'`, ""},
	{"Django", `tsp run %s -- 'python manage.py runserver $HOST:$PORT'`, ""},
	{"Flask", `tsp run %s -- 'flask run --port $PORT --host $HOST'`, ""},
	{"Rails", `tsp run %s -- 'bin/rails server -p $PORT -b $HOST'`, ""},
	{"PHP", `tsp run %s -- 'php -S $HOST:$PORT -t public'`, ""},
	{"Node", `tsp run %s -- node server.js`, "reads process.env.PORT"},
	{"Bun", `tsp run %s -- bun --hot src/index.ts`, "reads PORT"},
	{"Deno", `tsp run %s -- deno run -A --watch main.ts`, "reads PORT"},
	{"Go", `tsp run %s -- go run .`, "reads os.Getenv(\"PORT\")"},
	{"Any binary", `tsp run %s -- ./server`, "reads PORT and HOST"},
}

// examplesFor fills the examples in with name (or "<name>").
func examplesFor(name string) []runExample {
	if name == "" {
		name = "<name>"
	}
	out := make([]runExample, len(runExamples))
	for i, ex := range runExamples {
		out[i] = runExample{Stack: ex.Stack, Command: fmt.Sprintf(ex.Command, name), Note: ex.Note}
	}
	return out
}

// examplesText is the examples as aligned plain text, one per line.
func examplesText(name string) string {
	exs := examplesFor(name)
	width := 0
	for _, ex := range exs {
		if ex.Note != "" {
			width = max(width, len(ex.Command))
		}
	}
	var b strings.Builder
	for _, ex := range exs {
		line := fmt.Sprintf("  %-11s %s", ex.Stack, ex.Command)
		if ex.Note != "" {
			line = fmt.Sprintf("  %-11s %-*s  # %s", ex.Stack, width, ex.Command, ex.Note)
		}
		b.WriteString(strings.TrimRight(line, " ") + "\n")
	}
	return b.String()
}

// wantsHTML reports whether the client is a browser (it asks for text/html).
func wantsHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// hostOnly is a request's host, lowercased, without its port or trailing dot.
func hostOnly(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.TrimSuffix(strings.Trim(strings.ToLower(host), "[]"), ".")
}

// httpTarget is where http://host goes over https: the same URL for a
// .localhost name (the reserved ones too), the dashboard for the bare
// localhost and the loopback addresses; "" for any other host.
func httpTarget(host, uri string) string {
	switch name, ok := strings.CutSuffix(host, "."+localTLD); {
	case host == localTLD || host == "127.0.0.1" || host == "::1":
		return dashboardURL + uri
	case ok && len(name) <= 200 && nameRe.MatchString(name):
		return "https://" + host + uri
	}
	return ""
}

// redirectHandler answers http:// on port 80 with what https answers: a
// .localhost name moves to https with 308 (a browser then shows the very same
// page). A host outside .localhost gets a 404 that names nothing that runs: a
// site whose name was re-pointed at 127.0.0.1 (DNS rebinding) must not read what
// this machine serves.
func redirectHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := hostOnly(r.Host)
		target := httpTarget(host, r.URL.RequestURI())
		if target == "" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, "tsp: http://%s reached tsp's http port on this machine, but tsp serves only https://<name>.%s names: open %s for what runs\n", safeText(host), localTLD, dashboardURL)
			return
		}
		w.Header().Set("Location", target)
		if wantsHTML(r) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusPermanentRedirect)
			_ = pageTemplates.ExecuteTemplate(w, "moved", target)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusPermanentRedirect)
		fmt.Fprintf(w, "tsp: http://%s is served over https: %s\n(curl -L follows the redirect)\n", safeText(host), target)
	})
}

// ---------------------------------------------------------------- unknown names (404)

type notFoundPage struct {
	Name     string // the requested name, "" for the bare host
	URL      string
	Running  []localRoute
	Examples []runExample
	Docs     string
	Board    string
}

func newNotFoundPage(running []localRoute, name string) notFoundPage {
	p := notFoundPage{Name: name, Running: running, Examples: examplesFor(name), Docs: docsURL, Board: dashboardURL}
	if name != "" {
		p.URL = localRoute{Name: name}.URL()
	}
	return p
}

// writeNotFound answers a name no run registered: what runs, and how to run an
// app under this name, for any stack.
func writeNotFound(w http.ResponseWriter, r *http.Request, running []localRoute, name string) {
	p := newNotFoundPage(running, name)
	if wantsHTML(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_ = pageTemplates.ExecuteTemplate(w, "notfound", p)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	writeNotFoundText(w, p)
}

func writeNotFoundText(w io.Writer, p notFoundPage) {
	shown := p.Name
	if shown == "" {
		shown = "<name>"
	}
	if p.URL != "" {
		fmt.Fprintf(w, "tsp: nothing runs at %s\n\n", p.URL)
	}
	writeRunningText(w, p.Running)
	fmt.Fprintf(w, "\nServe an app at https://%s.%s:\n", shown, localTLD)
	fmt.Fprintf(w, "  just start it  tsp finds dev servers by itself and names each after its folder;\n")
	fmt.Fprintf(w, "                 to pick the name, put .tsp.json in the project: { \"name\": \"%s\" }\n", shown)
	fmt.Fprintf(w, "                 (or \"tsp\" in package.json / deno.json, [tool.tsp] in pyproject.toml,\n")
	fmt.Fprintf(w, "                 [package.metadata.tsp] in Cargo.toml, \"extra\".\"tsp\" in composer.json)\n")
	fmt.Fprintf(w, "  or through tsp tsp run %s -- '<command>' sets PORT and HOST for it:\n\n", shown)
	fmt.Fprint(w, examplesText(shown))
	fmt.Fprintf(w, "\nThe server must listen on $PORT and $HOST: tsp sets both. Quote a command that uses them,\nso they expand when it runs.\n")
	fmt.Fprintf(w, "Dashboard: %s   Docs: %s\n", p.Board, p.Docs)
}

func writeRunningText(w io.Writer, routes []localRoute) {
	if len(routes) == 0 {
		fmt.Fprintln(w, "No apps are running.")
		return
	}
	fmt.Fprintln(w, "Running:")
	for _, rt := range routes {
		fmt.Fprintf(w, "  %s  →  127.0.0.1:%d  (%s)\n", rt.URL(), rt.Port, rt.Dir)
	}
}

// ---------------------------------------------------------------- an app that does not answer (502)

type badGatewayPage struct {
	Route   localRoute
	Err     string
	Example string
	Board   string
}

// writeBadGateway answers a registered name whose app refused or dropped the
// request: where it should be listening, and the likely reasons.
func writeBadGateway(w http.ResponseWriter, r *http.Request, rt localRoute, err error) {
	p := badGatewayPage{Route: rt, Err: err.Error(), Example: fmt.Sprintf(runExamples[0].Command, rt.Name), Board: dashboardURL}
	if wantsHTML(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		_ = pageTemplates.ExecuteTemplate(w, "badgateway", p)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusBadGateway)
	fmt.Fprintf(w, "tsp: %s is registered but its app is not answering\n", rt.URL())
	fmt.Fprintf(w, "  expected on 127.0.0.1:%d (PORT=%d HOST=127.0.0.1), run by pid %d in %s\n", rt.Port, rt.Port, rt.PID, rt.Dir)
	fmt.Fprintf(w, "  %s\n\n", p.Err)
	fmt.Fprintln(w, "Likely:")
	fmt.Fprintln(w, "  - it is still starting: reload in a moment")
	fmt.Fprintf(w, "  - it does not listen on $PORT and $HOST: start it with them, e.g. %s\n", p.Example)
	fmt.Fprintln(w, "  - it stopped: see the terminal running tsp run")
	fmt.Fprintf(w, "Events: tsp logs   Dashboard: %s\n", dashboardURL)
}

// ---------------------------------------------------------------- the dashboard

// appStatus is one running name, with its traffic since this proxy started.
type appStatus struct {
	Name     string    `json:"name"`
	URL      string    `json:"url"`
	Source   string    `json:"source"` // "run" (tsp run) or "found" (discovered)
	Runtime  string    `json:"runtime,omitempty"`
	Port     int       `json:"port"`
	PID      int       `json:"pid"`
	Dir      string    `json:"dir"`
	Started  time.Time `json:"started,omitzero"`
	Uptime   string    `json:"-"`
	Requests uint64    `json:"requests"`
	Failed   uint64    `json:"failed"`
	S4xx     uint64    `json:"4xx"`
	S5xx     uint64    `json:"5xx"`
	P50ms    float64   `json:"p50ms"`
	P95ms    float64   `json:"p95ms"`
}

// proxyStatus is the proxy's health, as `tsp stats` and `tsp doctor` report it.
type proxyStatus struct {
	Version         string            `json:"version"`              // the release serving
	Supervisor      string            `json:"supervisor,omitempty"` // the boot service's own release
	Mode            string            `json:"mode"`                 // worker, builtin, standalone
	PID             int               `json:"pid"`
	Since           time.Time         `json:"since,omitzero"` // when the supervisor started
	UptimeSeconds   float64           `json:"uptimeSeconds"`  // this process
	Uptime          string            `json:"-"`
	Swaps           int               `json:"swaps"`
	Rollbacks       int               `json:"rollbacks"`
	Crashes         int               `json:"crashes"`
	SetAside        map[string]string `json:"setAside,omitempty"` // release → why
	Discovery       watchStatus       `json:"discovery"`          // the watcher: on/off, interval, range, problems (routes are in apps)
	HTTPRedirect    bool              `json:"httpRedirect"`
	HTTPNote        string            `json:"httpNote,omitempty"`
	AutoUpdate      bool              `json:"autoUpdate"`
	UpdateCheckedAt time.Time         `json:"updateCheckedAt,omitzero"`
	UpdateChecked   string            `json:"-"`
	Latest          string            `json:"latest,omitempty"`
}

// dashboardStatus is https://tsp.localhost/api/status, and what the page shows.
type dashboardStatus struct {
	Proxy    proxyStatus  `json:"proxy"`
	Apps     []appStatus  `json:"apps"`
	Examples []runExample `json:"-"`
	Docs     string       `json:"docs"`
}

func newDashboardStatus(dir string, reg *localRegistry, w *localWatcher, m *proxyMetrics) dashboardStatus {
	snap := m.snapshot()
	now := time.Now()
	st, _ := readProxyState(dir)
	ps := proxyStatus{
		Version: Version, Supervisor: st.Launcher, Mode: st.Mode, PID: os.Getpid(), Since: st.Started,
		UptimeSeconds: snap.Process.Uptime, Uptime: shortDuration(time.Duration(snap.Process.Uptime * float64(time.Second))),
		Swaps: st.Swaps, Rollbacks: st.Rollbacks, Crashes: st.Crashes, SetAside: loadBinState(dir).Bad,
		HTTPRedirect: st.HTTPPort > 0, HTTPNote: httpRedirectNote(st), AutoUpdate: autoUpdateEnabled(),
		Discovery: w.status(),
	}
	ps.Discovery.Routes = nil
	if ps.Mode == "" {
		ps.Mode = "standalone"
	}
	if us := loadUpdateState(dir); !us.CheckedAt.IsZero() {
		ps.UpdateCheckedAt, ps.UpdateChecked = us.CheckedAt, shortDuration(now.Sub(us.CheckedAt))+" ago"
		if latest := normalizeVer(us.Latest); latest != "" {
			if versionLess(latest, Version) {
				latest = normalizeVer(Version)
			}
			ps.Latest = latest
		}
	}
	ds := dashboardStatus{Proxy: ps, Apps: []appStatus{}, Examples: examplesFor(""), Docs: docsURL}
	for _, rt := range allRoutes(reg, w) {
		a := appStatus{Name: rt.Name, URL: rt.URL(), Source: rt.Source, Runtime: rt.Runtime, Port: rt.Port, PID: rt.PID, Dir: rt.Dir}
		if fi, err := os.Stat(reg.file(rt.Name)); rt.Source == sourceRun && err == nil {
			a.Started, a.Uptime = fi.ModTime(), shortDuration(now.Sub(fi.ModTime()))
		}
		if rs, ok := snap.Routes[rt.Name]; ok {
			a.Requests, a.Failed, a.S4xx, a.S5xx = rs.Requests, rs.Errors, rs.S4xx, rs.S5xx
			a.P50ms = float64(rs.percentile(0.50).Microseconds()) / 1000
			a.P95ms = float64(rs.percentile(0.95).Microseconds()) / 1000
		}
		ds.Apps = append(ds.Apps, a)
	}
	return ds
}

// httpRedirectNote says why http://*.localhost does not redirect, if it does not.
func httpRedirectNote(st proxyState) string {
	switch {
	case st.HTTPPort > 0:
		return ""
	case st.HTTPError != "":
		return "port 80 is taken (" + st.HTTPError + "); lsof -nP -iTCP:80 -sTCP:LISTEN shows which program"
	case st.Mode == "standalone":
		return "a tsp proxy started by hand serves https only"
	default:
		return "the boot service predates it; tsp service install adds it (sudo once)"
	}
}

func readProxyState(dir string) (proxyState, bool) {
	var st proxyState
	data, err := os.ReadFile(proxyStatePath(dir))
	if err != nil || json.Unmarshal(data, &st) != nil {
		return proxyState{}, false
	}
	return st, true
}

// dashboardHandler answers https://tsp.localhost: the page at /, the JSON at
// /api/status, and one app's stop (GET /api/apps/{name} says what a stop would
// end and how, POST /api/apps/{name}/stop ends it). Only the dashboard's own page
// can stop an app from a browser: Go's cross-origin protection refuses a POST
// another site sends (Sec-Fetch-Site, Origin), and the JSON body a form cannot.
// A stop must also come from a process of this user (requestFromOwner), not from
// another account on the same machine.
func dashboardHandler(dir string, reg *localRegistry, lw *localWatcher, m *proxyMetrics) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, newDashboardStatus(dir, reg, lw, m))
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = pageTemplates.ExecuteTemplate(w, "dashboard", newDashboardStatus(dir, reg, lw, m))
	})
	mux.HandleFunc("GET /api/apps/{name}", func(w http.ResponseWriter, r *http.Request) {
		t, code, err := dashboardTarget(dir, reg, lw, r.PathValue("name"))
		if err != nil {
			writeJSON(w, code, stopResult{Error: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, newStopPlan(t))
	})
	mux.HandleFunc("POST /api/apps/{name}/stop", func(w http.ResponseWriter, r *http.Request) {
		if mt, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";"); strings.TrimSpace(mt) != "application/json" {
			writeJSON(w, http.StatusUnsupportedMediaType, stopResult{Error: `send Content-Type: application/json and a body like {"force": false}`})
			return
		}
		var body struct {
			Force bool `json:"force"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, stopResult{Error: "the body is not JSON: " + err.Error()})
			return
		}
		if err := requestFromOwner(r.RemoteAddr); err != nil {
			writeJSON(w, http.StatusForbidden, stopResult{Error: err.Error()})
			return
		}
		t, code, err := dashboardTarget(dir, reg, lw, r.PathValue("name"))
		if err != nil {
			writeJSON(w, code, stopResult{Error: err.Error()})
			return
		}
		res := stopProcesses(t, body.Force)
		appLog().Info("app stopped from the dashboard", "name", t.Name, "pid", t.PID, "force", body.Force, "stopped", res.Stopped, "took", res.Took, "err", res.Error)
		if res.Stopped && t.Source == sourceFound {
			lw.forget(t.Name) // the list drops it now, not after the next scans
		}
		writeJSON(w, map[bool]int{true: http.StatusOK, false: http.StatusConflict}[res.Stopped], res)
	})
	return http.NewCrossOriginProtection().Handler(mux)
}

// dashboardTarget is the stop target of a name the proxy serves now, or the
// status code and reason it has none.
func dashboardTarget(dir string, reg *localRegistry, lw *localWatcher, name string) (stopTarget, int, error) {
	for _, rt := range allRoutes(reg, lw) {
		if rt.Name != name {
			continue
		}
		t, err := targetOf(reg, rt)
		if err == nil {
			err = guardTarget(dir, t)
		}
		if err != nil {
			return t, http.StatusConflict, err
		}
		return t, http.StatusOK, nil
	}
	return stopTarget{}, http.StatusNotFound, fmt.Errorf("nothing runs at %s", localRoute{Name: name}.URL())
}

// stopPlan is what the dashboard shows before a stop: what it ends, the command
// each button runs, and the same commands for a terminal, for this OS.
type stopPlan struct {
	URL      string        `json:"url,omitempty"`
	Target   stopTarget    `json:"target"`
	Folder   string        `json:"folder,omitempty"`
	How      string        `json:"how"`   // what Stop does, in words
	Runs     string        `json:"runs"`  // what Stop runs
	Force    string        `json:"force"` // what Force stop runs
	Commands []stopCommand `json:"commands"`
}

func newStopPlan(t stopTarget) stopPlan {
	how := fmt.Sprintf("Stop sends SIGTERM, as Ctrl-C in its terminal would, and waits up to %s for it to exit.", stopGrace)
	if runtime.GOOS == "windows" {
		how = fmt.Sprintf("Stop asks it and the processes it started to close (taskkill /T), and waits up to %s.", stopGrace)
	}
	if t.Source == sourceRun {
		how += " It was started with tsp run, which passes the stop on to its app and ends with it."
	}
	p := stopPlan{Target: t, How: how, Runs: signalCommand(t, false), Force: signalCommand(t, true), Commands: stopCommands(t)}
	if t.Name != "" {
		p.URL = localRoute{Name: t.Name}.URL()
	}
	if t.Dir != "" {
		p.Folder = homeShort(t.Dir)
	}
	return p
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	out, _ := json.MarshalIndent(v, "", "  ")
	_, _ = w.Write(append(out, '\n'))
}

// shortDuration is 45s, 12m, 3h12m or 2d4h.
func shortDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}

// homeShort shows a folder under the home as ~/…
func homeShort(dir string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, dir); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.Join("~", rel)
		}
	}
	return dir
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// pageTemplates are the HTML answers. html/template escapes every value.
var pageTemplates = template.Must(template.New("pages").Funcs(template.FuncMap{
	"home": homeShort,
	"ms":   func(v float64) string { return fmtMs(v) },
	"keys": sortedKeys,
}).Parse(pageHTML))

const pageHTML = `
{{define "style"}}<meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta name="color-scheme" content="light dark">
<style>
:root{--bg:#fcfcfd;--fg:#1d2430;--muted:#5f6878;--line:#e3e6eb;--soft:#f3f5f8;--accent:#2f6fdb;--ok:#1f8f63;--warn:#b26a00;--bad:#c4372f}
@media (prefers-color-scheme:dark){:root{--bg:#14181f;--fg:#e6e9ee;--muted:#9aa3b2;--line:#2b313b;--soft:#1b2029;--accent:#78a6ff;--ok:#5fd0a0;--warn:#f0b45a;--bad:#e5534b}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--fg);font:15px/1.6 ui-sans-serif,system-ui,-apple-system,"Segoe UI",sans-serif}
main{max-width:980px;margin:0 auto;padding:40px 22px 64px}h1{font-size:28px;line-height:1.15;margin:0 0 6px;letter-spacing:-.02em}
h2{font-size:17px;margin:34px 0 10px}p{margin:8px 0}a{color:var(--accent)}code,pre,.mono{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:13px}
pre{background:var(--soft);border:1px solid var(--line);border-radius:8px;padding:12px 14px;overflow-x:auto;margin:10px 0}
table{width:100%;border-collapse:collapse;font-size:14px}th,td{text-align:left;padding:8px 10px;border-bottom:1px solid var(--line);vertical-align:top}
th{color:var(--muted);font-weight:600;font-size:13px}.n{text-align:right;font-variant-numeric:tabular-nums}h1 .mono{font-size:.8em}.muted{color:var(--muted)}
.ok{color:var(--ok)}.warn{color:var(--warn)}dl{display:grid;grid-template-columns:max-content 1fr;gap:6px 18px;margin:0}dt{color:var(--muted)}dd{margin:0}
.wrap{overflow-x:auto}footer{margin-top:40px;color:var(--muted);font-size:13px}
button{font:inherit;font-size:13px;border:1px solid var(--line);background:var(--bg);color:var(--fg);border-radius:6px;padding:4px 10px;cursor:pointer}
button:hover{background:var(--soft)}button:focus-visible{outline:2px solid var(--accent);outline-offset:2px}
button.danger{background:var(--bad);border-color:var(--bad);color:#fff}button.danger:hover{filter:brightness(1.08)}
td .stop{padding:2px 9px;color:var(--muted)}td .stop:hover{color:var(--bad);border-color:var(--bad)}
dialog{border:1px solid var(--line);border-radius:12px;background:var(--bg);color:var(--fg);padding:0;width:min(640px,calc(100vw - 32px));box-shadow:0 24px 60px rgba(0,0,0,.28)}
dialog::backdrop{background:rgba(10,14,20,.45)}dialog form{padding:22px 24px 20px}dialog h2{margin:0 0 6px;font-size:18px;word-break:break-all}
dialog dl{margin:14px 0}dialog dd{word-break:break-all}.cmds{display:grid;grid-template-columns:max-content 1fr max-content;gap:6px 10px;align-items:center;margin:8px 0 0}
.cmds code{background:var(--soft);border:1px solid var(--line);border-radius:6px;padding:4px 8px;overflow-x:auto;white-space:nowrap}.cmds span{color:var(--muted);font-size:13px}
.runs{margin:10px 0 0}.runs code{background:var(--soft);border-radius:4px;padding:1px 6px}.actions{display:flex;gap:8px;justify-content:flex-end;margin-top:18px}
#sd-msg.bad{color:var(--bad)}#sd-msg.good{color:var(--ok)}
dialog:not([data-state=ready]) #sd-stop,dialog:not([data-state=failed]) #sd-force,dialog:not([data-state=ready]):not([data-state=failed]) .plan{display:none}
</style>{{end}}

{{define "examples"}}<div class="wrap"><table><tbody>{{range .}}<tr><td>{{.Stack}}</td><td class="mono">{{.Command}}</td><td class="muted">{{.Note}}</td></tr>{{end}}</tbody></table></div>{{end}}

{{define "running"}}{{if .}}<div class="wrap"><table><thead><tr><th>Name</th><th>From</th><th>Port</th><th>Folder</th></tr></thead><tbody>
{{range .}}<tr><td><a href="{{.URL}}">{{.URL}}</a></td><td>{{.Source}}</td><td class="mono">{{.Port}}</td><td class="mono">{{home .Dir}}</td></tr>{{end}}
</tbody></table></div>{{else}}<p class="muted">No apps are running.</p>{{end}}{{end}}

{{define "moved"}}<!doctype html><html lang="en"><head>{{template "style"}}<title>Moved to {{.}}</title></head><body><main>
<h1>Served over https</h1><p>This address is at <a href="{{.}}">{{.}}</a>.</p></main></body></html>{{end}}

{{define "notfound"}}<!doctype html><html lang="en"><head>{{template "style"}}<title>Nothing runs at {{if .URL}}{{.URL}}{{else}}this name{{end}}</title></head><body><main>
<h1>Nothing runs at {{if .URL}}<span class="mono">{{.URL}}</span>{{else}}this name{{end}}</h1>
<p class="muted">The local HTTPS proxy is up; no app is registered under this name.</p>
<h2>Running now</h2>{{template "running" .Running}}
<h2>Serve an app here</h2>
<p>Start it as usual: tsp finds dev servers by itself and names each after its folder. To pick the name, put <code>.tsp.json</code> in the project:</p>
<pre>{ "name": "{{if .Name}}{{.Name}}{{else}}&lt;name&gt;{{end}}" }</pre>
<p>or a <code>"tsp"</code> section in the file your stack already has: package.json, deno.json, pyproject.toml (<code>[tool.tsp]</code>), Cargo.toml (<code>[package.metadata.tsp]</code>), composer.json (<code>"extra"</code>). Or start it through tsp, which sets <code>$PORT</code> and <code>$HOST</code>:</p>
{{template "examples" .Examples}}
<p>The server must listen on <code>$PORT</code> and <code>$HOST</code>: tsp sets both. Quote a command that uses them, so they expand when it runs.</p>
<footer><a href="{{.Board}}">All names: {{.Board}}</a> &nbsp; <a href="{{.Docs}}">Docs</a></footer>
</main></body></html>{{end}}

{{define "badgateway"}}<!doctype html><html lang="en"><head>{{template "style"}}<title>{{.Route.URL}} is not answering</title></head><body><main>
<h1><span class="mono">{{.Route.URL}}</span> is not answering</h1>
<p class="muted">It is registered, but nothing answered on its port.</p>
<dl><dt>Expected on</dt><dd class="mono">127.0.0.1:{{.Route.Port}} (PORT={{.Route.Port}} HOST=127.0.0.1)</dd>
<dt>Run by</dt><dd class="mono">pid {{.Route.PID}} in {{home .Route.Dir}}</dd><dt>Error</dt><dd class="mono">{{.Err}}</dd></dl>
<h2>Likely</h2><ul><li>It is still starting: reload in a moment.</li>
<li>It does not listen on <code>$PORT</code> and <code>$HOST</code>: start it with them, e.g. <code>{{.Example}}</code>.</li>
<li>It stopped: see the terminal running <code>tsp run</code>, and <code>tsp logs</code>.</li></ul>
<footer><a href="{{.Board}}">All names: {{.Board}}</a></footer>
</main></body></html>{{end}}

{{define "dashboard"}}<!doctype html><html lang="en"><head>{{template "style"}}<title>tsp: local HTTPS names</title></head><body><main>
<h1>Local HTTPS names</h1>
<p class="muted">Served by tsp {{.Proxy.Version}} on this machine. Updates by itself every few seconds.</p>
<div id="live">
<h2>Running</h2>
{{if .Apps}}<div class="wrap"><table><thead><tr><th>Name</th><th>From</th><th>Port</th><th>PID</th><th>Folder</th><th>Up</th><th class="n">Requests</th><th class="n">Failed</th><th class="n">p50</th><th class="n">p95</th><th><span class="muted" aria-hidden="true"></span></th></tr></thead><tbody>
{{range .Apps}}<tr><td><a href="{{.URL}}">{{.URL}}</a></td><td>{{.Source}}{{if .Runtime}} <span class="muted">{{.Runtime}}</span>{{end}}</td><td class="mono">{{.Port}}</td><td class="mono">{{.PID}}</td><td class="mono">{{home .Dir}}</td><td>{{.Uptime}}</td><td class="n">{{.Requests}}</td><td class="n">{{.Failed}}</td><td class="n">{{ms .P50ms}}</td><td class="n">{{ms .P95ms}}</td><td>{{if and .PID (ne .Runtime "docker")}}<button type="button" class="stop" data-stop="{{.Name}}" aria-label="Stop {{.URL}}">Stop</button>{{end}}</td></tr>{{end}}
</tbody></table></div><p class="muted"><b>run</b>: started with tsp run; <b>found</b>: a dev server tsp found by itself. Requests since this proxy started; p50 and p95 are times to the first byte. <b>Stop</b> shows what it ends and the command it runs before it does.</p>
{{else}}<p class="muted">No apps are running. Start one below.</p>{{end}}
<h2>Proxy</h2><dl>
<dt>Serving</dt><dd>tsp {{.Proxy.Version}} ({{.Proxy.Mode}}, pid {{.Proxy.PID}}), up {{.Proxy.Uptime}}{{if .Proxy.Supervisor}}; boot service tsp {{.Proxy.Supervisor}}{{end}}</dd>
<dt>Changes</dt><dd>{{.Proxy.Swaps}} live updates, {{.Proxy.Rollbacks}} set aside, {{.Proxy.Crashes}} crashes</dd>
{{with .Proxy.SetAside}}<dt>Set aside</dt><dd>{{range $v := keys .}}<div class="mono">{{$v}}: {{index $.Proxy.SetAside $v}}</div>{{end}}</dd>{{end}}
<dt>Discovery</dt><dd>{{if .Proxy.Discovery.Enabled}}finds dev servers on ports {{.Proxy.Discovery.Range}}: scans every {{.Proxy.Discovery.Every}}, and at once for a name it does not know{{else}}off: only tsp run apps (tsp configure --discover turns it on){{end}}</dd>
{{range .Proxy.Discovery.Problems}}<dt>Skipped</dt><dd class="warn">{{.}}</dd>{{end}}
<dt>http://</dt><dd>{{if .Proxy.HTTPRedirect}}<span class="ok">redirects to https</span>: http://&lt;name&gt;.localhost opens the same page over https{{else}}<span class="warn">not redirected</span>: {{.Proxy.HTTPNote}}{{end}}</dd>
<dt>Updates</dt><dd>{{if .Proxy.AutoUpdate}}automatic{{if .Proxy.UpdateChecked}}, checked {{.Proxy.UpdateChecked}}{{end}}{{if .Proxy.Latest}}, latest {{.Proxy.Latest}}{{end}}{{else}}off (tsp configure --auto-update turns them on){{end}}</dd>
</dl>
</div>
<h2>Run an app</h2>
<p>Start it as usual: tsp finds it and serves it at <code>https://&lt;folder&gt;.localhost</code>. Name it in <code>.tsp.json</code> (<code>{ "name": "api.acme" }</code>), or in the file your stack has: package.json, deno.json, pyproject.toml <code>[tool.tsp]</code>, Cargo.toml, composer.json. Or start it through tsp:</p>
{{template "examples" .Examples}}
<p>The server must listen on <code>$PORT</code> and <code>$HOST</code>: tsp sets both.</p>
<footer>JSON for scripts: <a href="/api/status">/api/status</a> &nbsp; <a href="{{.Docs}}">Docs</a> &nbsp; <a href="https://tailscaleproxy.vercel.app/logs-and-stats">Logs and stats</a></footer>
</main>
<dialog id="stop-dialog" aria-labelledby="sd-title" data-state="checking"><form method="dialog">
<h2 id="sd-title">Stop</h2>
<p id="sd-msg" role="status" aria-live="polite"></p>
<div class="plan"><dl id="sd-facts"></dl>
<p class="runs">Stop runs <code id="sd-runs"></code></p>
<p class="muted" style="margin-top:14px">Or from a terminal:</p><div class="cmds" id="sd-cmds"></div></div>
<div class="actions"><button value="cancel" id="sd-cancel">Cancel</button><button type="button" class="danger" id="sd-stop">Stop</button><button type="button" class="danger" id="sd-force">Force stop</button></div>
</form></dialog>
<script>
(() => {
  const dlg = document.getElementById("stop-dialog");
  const el = (id) => document.getElementById(id);
  let plan = null;
  const state = (s, msg, tone) => {
    dlg.dataset.state = s;
    el("sd-msg").textContent = msg;
    el("sd-msg").className = tone || "";
    el("sd-cancel").textContent = s === "ready" || s === "failed" ? "Cancel" : "Close";
  };
  const facts = (rows) => el("sd-facts").replaceChildren(...rows.filter((r) => r[1]).flatMap(([k, v]) => {
    const dt = document.createElement("dt"), dd = document.createElement("dd");
    dt.textContent = k; dd.textContent = v; dd.className = "mono";
    return [dt, dd];
  }));
  const commands = (cmds) => el("sd-cmds").replaceChildren(...cmds.flatMap((c) => {
    const label = document.createElement("span"), code = document.createElement("code"), copy = document.createElement("button");
    label.textContent = c.label; code.textContent = c.command;
    copy.type = "button"; copy.textContent = "Copy"; copy.dataset.copy = c.command;
    copy.setAttribute("aria-label", "Copy " + c.command);
    return [label, code, copy];
  }));
  const openStop = async (name) => {
    plan = null;
    el("sd-title").textContent = "Stop https://" + name + ".localhost?";
    state("checking", "Checking what runs there…");
    dlg.showModal();
    const res = await fetch("/api/apps/" + encodeURIComponent(name), { cache: "no-store" }).catch(() => null);
    const body = res ? await res.json().catch(() => ({})) : { error: "the proxy did not answer: tsp doctor" };
    if (!res || !res.ok) return state("error", body.error || "it cannot be stopped from here", "bad");
    plan = body;
    const t = body.target;
    facts([
      ["Process", (t.command || "") + " (pid " + t.pid + ")"],
      ["Started", t.source === "run" ? "with tsp run" : t.source === "found" ? "by you; tsp found it" : ""],
      ["Port", t.port ? String(t.port) : ""],
      ["Folder", body.folder],
    ]);
    el("sd-runs").textContent = body.runs;
    commands(body.commands);
    state("ready", body.how);
    el("sd-stop").focus();
  };
  const stop = async (force) => {
    if (!plan) return;
    state("stopping", force ? "Ending it now…" : "Stopping… waiting for it to exit.");
    const res = await fetch("/api/apps/" + encodeURIComponent(plan.target.name) + "/stop", {
      method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ force }),
    }).catch(() => null);
    const body = res ? await res.json().catch(() => ({})) : { error: "the proxy did not answer: tsp doctor" };
    if (body.stopped) {
      state("done", "Stopped in " + body.took + " (" + body.ran + ").", "good");
      setTimeout(() => { if (dlg.dataset.state === "done") dlg.close(); refresh(); }, 1200);
      return;
    }
    if (res && res.status === 409 && !force && plan) {
      el("sd-runs").textContent = plan.force;
      return state("failed", (body.error || "it did not stop") + ". It may still be finishing work, or it ignores SIGTERM: Force stop ends it at once.", "bad");
    }
    state("error", body.error || "it did not stop", "bad");
  };
  const refresh = async () => {
    if (dlg.open || document.hidden) return;
    const res = await fetch("/", { cache: "no-store" }).catch(() => null);
    if (!res || !res.ok) return;
    const live = new DOMParser().parseFromString(await res.text(), "text/html").getElementById("live");
    if (live) el("live").replaceWith(live);
  };
  el("sd-stop").addEventListener("click", () => stop(false));
  el("sd-force").addEventListener("click", () => stop(true));
  dlg.addEventListener("close", refresh);
  document.addEventListener("click", (e) => {
    const b = e.target.closest("[data-stop]");
    if (b) openStop(b.dataset.stop);
    const c = e.target.closest("[data-copy]");
    if (c) navigator.clipboard.writeText(c.dataset.copy).then(() => {
      c.textContent = "Copied";
      setTimeout(() => { c.textContent = "Copy"; }, 1200);
    });
  });
  setInterval(refresh, 5000);
  document.addEventListener("visibilitychange", refresh);
})();
</script>
</body></html>{{end}}
`
