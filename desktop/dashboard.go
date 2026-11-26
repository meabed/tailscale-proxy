package main

import (
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/meabed/tailscale-proxy/core"
)

//go:embed assets/panel.html
var panelHTML string

//go:embed assets/settings.html
var settingsHTML string

// procStat is one process's resource use (procstats_*.go).
type procStat struct {
	CPU    float64 // percent
	MemMB  int     // resident set, MB
	Uptime string  // compact process age, e.g. "2h13m"
}

type svcJSON struct {
	Slug         string  `json:"slug"`
	Runtime      string  `json:"runtime"`
	Port         int     `json:"port"`
	PID          int     `json:"pid"`
	Dir          string  `json:"dir"`
	URL          string  `json:"url"`
	CPU          float64 `json:"cpu"`
	MemMB        int     `json:"memMB"`
	Uptime       string  `json:"uptime"`
	DiscoveredAt int64   `json:"discoveredAt"` // unix seconds, 0 if unknown
}

type statusJSON struct {
	Running    bool                 `json:"running"`
	Mode       string               `json:"mode"`
	Private    bool                 `json:"private"`
	Node       string               `json:"node"`
	PublicBase string               `json:"publicBase"`
	HTTPSPort  int                  `json:"httpsPort"`
	Err        string               `json:"err"`
	Services   []svcJSON            `json:"services"`
	Tailscale  core.TailscaleHealth `json:"tailscale"`
}

type configJSON struct {
	Config    core.Config `json:"config"`
	Runtimes  []string    `json:"runtimes"`
	HideDock  bool        `json:"hideDock"`
	Autostart bool        `json:"autostart"`
	Name      string      `json:"name"`
	Version   string      `json:"version"`
}

func (u *ui) status() statusJSON {
	st := u.ctl.Status()
	u.mu.Lock()
	private := u.cfg.Private
	u.mu.Unlock()
	out := statusJSON{
		Running: st.Running, Mode: st.Mode, Private: private, Node: st.Node,
		PublicBase: st.PublicBase, HTTPSPort: st.HTTPSPort, Err: st.Err,
		Services: []svcJSON{}, Tailscale: u.cachedHealth(),
	}

	pids := make([]int, 0, len(st.Services))
	for _, s := range st.Services {
		if s.PID > 0 {
			pids = append(pids, s.PID)
		}
	}
	stats := u.cachedStats(pids)
	seen := u.markSeen(st.Services)

	for _, s := range st.Services {
		row := svcJSON{
			Slug: s.Slug, Runtime: s.Runtime, Port: s.Port, PID: s.PID, Dir: s.Dir, URL: s.URL,
			DiscoveredAt: seen[s.Slug],
		}
		if ps, ok := stats[s.PID]; ok {
			row.CPU, row.MemMB, row.Uptime = ps.CPU, ps.MemMB, ps.Uptime
		}
		out.Services = append(out.Services, row)
	}
	return out
}

// serviceHasPID / serviceHasDir gate the destructive/open actions to processes
// and folders that actually belong to a currently-discovered service.
func (u *ui) serviceHasPID(pid int) bool {
	if pid <= 0 {
		return false
	}
	for _, s := range u.ctl.Status().Services {
		if s.PID == pid {
			return true
		}
	}
	return false
}

func (u *ui) serviceHasDir(dir string) bool {
	for _, s := range u.ctl.Status().Services {
		if s.Dir != "" && s.Dir == dir {
			return true
		}
	}
	return false
}

// cachedHealth probes tailscale at most every 4s.
func (u *ui) cachedHealth() core.TailscaleHealth {
	u.dmu.Lock()
	defer u.dmu.Unlock()
	if time.Since(u.healthAt) > 4*time.Second {
		u.health = core.CheckTailscale()
		u.healthAt = time.Now()
	}
	return u.health
}

// cachedStats batches one ps call for the pids at most every 2s.
func (u *ui) cachedStats(pids []int) map[int]procStat {
	u.dmu.Lock()
	defer u.dmu.Unlock()
	if u.stats == nil || time.Since(u.statsAt) > 2*time.Second {
		u.stats = procStats(pids)
		u.statsAt = time.Now()
	}
	return u.stats
}

// markSeen records first-discovery time per slug and returns slug→unix-seconds.
func (u *ui) markSeen(svcs []core.ServiceURL) map[string]int64 {
	u.dmu.Lock()
	defer u.dmu.Unlock()
	if u.seen == nil {
		u.seen = map[string]time.Time{}
	}
	live := map[string]bool{}
	out := map[string]int64{}
	for _, s := range svcs {
		live[s.Slug] = true
		if _, ok := u.seen[s.Slug]; !ok {
			u.seen[s.Slug] = time.Now()
		}
		out[s.Slug] = u.seen[s.Slug].Unix()
	}
	for slug := range u.seen {
		if !live[slug] {
			delete(u.seen, slug)
		}
	}
	return out
}

func (u *ui) configState() configJSON {
	u.mu.Lock()
	cfg := u.cfg
	u.mu.Unlock()
	return configJSON{
		Config: cfg, Runtimes: core.KnownRuntimes(), HideDock: loadPrefs().HideDock,
		Autostart: autostartEnabled(), Name: appName, Version: core.Version,
	}
}

// startDashboard serves the panel + settings pages and a token-gated control API
// on a random loopback port. The token (injected into the HTML) stops other
// local processes/browsers from driving the app.
func (u *ui) startDashboard() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)

	html := func(page string) http.HandlerFunc {
		body := []byte(strings.ReplaceAll(page, "__TOKEN__", token))
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(body)
		}
	}
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-TSP-Token") != token {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			h(w, r)
		}
	}
	// act runs a token-gated action and answers 204.
	act := func(f func(*http.Request)) http.HandlerFunc {
		return auth(func(w http.ResponseWriter, r *http.Request) {
			f(r)
			w.WriteHeader(http.StatusNoContent)
		})
	}
	decode := func(r *http.Request, v any) { _ = json.NewDecoder(r.Body).Decode(v) }
	writeJSON := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/{$}", html(panelHTML))
	mux.HandleFunc("/settings", html(settingsHTML))

	mux.HandleFunc("/api/status", auth(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, u.status()) }))
	mux.HandleFunc("/api/checkupdate", auth(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, core.CheckUpdate()) }))
	mux.HandleFunc("GET /api/config", auth(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, u.configState()) }))
	mux.HandleFunc("POST /api/config", act(func(r *http.Request) {
		var cfg core.Config
		if json.NewDecoder(r.Body).Decode(&cfg) == nil {
			go u.applyConfig(cfg)
		}
	}))
	mux.HandleFunc("/api/toggle", act(func(*http.Request) { go u.toggle() }))
	mux.HandleFunc("/api/refresh", act(func(*http.Request) { u.ctl.Refresh() }))
	mux.HandleFunc("/api/settings", act(func(*http.Request) { u.showSettings() }))
	mux.HandleFunc("/api/hidepanel", act(func(*http.Request) { u.hidePanel() }))
	mux.HandleFunc("/api/quit", act(func(*http.Request) { go u.quit() }))
	mux.HandleFunc("/api/mode", act(func(r *http.Request) {
		var b struct {
			Private bool `json:"private"`
		}
		decode(r, &b)
		go u.setPrivate(b.Private)
	}))
	mux.HandleFunc("/api/autostart", act(func(r *http.Request) {
		var b struct {
			On bool `json:"on"`
		}
		decode(r, &b)
		go u.setAutostart(b.On)
	}))
	mux.HandleFunc("/api/prefs", act(func(r *http.Request) {
		var b prefs
		decode(r, &b)
		_ = savePrefs(b)
	}))
	mux.HandleFunc("/api/open", act(func(r *http.Request) {
		var b struct {
			URL string `json:"url"`
		}
		decode(r, &b)
		if p, err := url.Parse(b.URL); err == nil && (p.Scheme == "http" || p.Scheme == "https") {
			openExternal(b.URL)
		}
	}))
	mux.HandleFunc("/api/openconfig", act(func(*http.Request) {
		if p, err := core.ConfigPath(); err == nil {
			openExternal(p)
		}
	}))
	mux.HandleFunc("/api/openfolder", act(func(r *http.Request) {
		var b struct {
			Path string `json:"path"`
		}
		decode(r, &b)
		if b.Path != "" && u.serviceHasDir(b.Path) { // only a discovered service's folder
			openExternal(b.Path)
		}
	}))
	mux.HandleFunc("/api/copy", act(func(r *http.Request) {
		var b struct {
			Text string `json:"text"`
		}
		decode(r, &b)
		if b.Text != "" {
			_ = copyToClipboard(b.Text)
		}
	}))
	mux.HandleFunc("/api/kill", act(func(r *http.Request) {
		var b struct {
			PID int `json:"pid"`
		}
		decode(r, &b)
		if u.serviceHasPID(b.PID) { // only a discovered service's process
			_ = killPID(b.PID)
			u.ctl.Refresh()
		}
	}))

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	return "http://" + ln.Addr().String(), nil
}
