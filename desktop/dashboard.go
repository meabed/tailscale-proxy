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

	"github.com/meabed/tailscale-proxy/core"
)

//go:embed assets/panel.html
var panelHTML string

type svcJSON struct {
	Slug    string `json:"slug"`
	Runtime string `json:"runtime"`
	Port    int    `json:"port"`
	URL     string `json:"url"`
}

type statusJSON struct {
	Running    bool      `json:"running"`
	Mode       string    `json:"mode"`
	Private    bool      `json:"private"`
	Node       string    `json:"node"`
	PublicBase string    `json:"publicBase"`
	Autostart  bool      `json:"autostart"`
	Err        string    `json:"err"`
	Services   []svcJSON `json:"services"`
}

func (u *ui) buildStatus() statusJSON {
	st := u.ctl.Status()
	u.mu.Lock()
	private := u.cfg.Private
	u.mu.Unlock()
	out := statusJSON{
		Running: st.Running, Mode: st.Mode, Private: private, Node: st.Node,
		PublicBase: st.PublicBase, Autostart: autostartEnabled(), Err: st.Err,
		Services: []svcJSON{},
	}
	for _, s := range st.Services {
		out.Services = append(out.Services, svcJSON{Slug: s.Slug, Runtime: s.Runtime, Port: s.Port, URL: s.URL})
	}
	return out
}

// startDashboard serves the tray panel + its control API on a random loopback
// port and returns the URL to load. API calls require the per-session token
// (set in the served HTML) so other local processes / browsers can't drive it.
func (u *ui) startDashboard() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	u.token = hex.EncodeToString(buf)
	html := strings.ReplaceAll(panelHTML, "__TOKEN__", u.token)

	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-TSP-Token") != u.token {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			h(w, r)
		}
	}
	decode := func(r *http.Request, v any) { _ = json.NewDecoder(r.Body).Decode(v) }

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	})
	mux.HandleFunc("/api/status", auth(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(u.buildStatus())
	}))
	mux.HandleFunc("/api/toggle", auth(func(w http.ResponseWriter, r *http.Request) {
		go u.toggle()
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("/api/mode", auth(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Private bool `json:"private"`
		}
		decode(r, &b)
		go u.setPrivate(b.Private)
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("/api/autostart", auth(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			On bool `json:"on"`
		}
		decode(r, &b)
		go u.setAutostart(b.On)
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("/api/open", auth(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			URL string `json:"url"`
		}
		decode(r, &b)
		if parsed, err := url.Parse(b.URL); err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") {
			openExternal(b.URL)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("/api/openconfig", auth(func(w http.ResponseWriter, r *http.Request) {
		if p, err := core.ConfigPath(); err == nil {
			openExternal(p)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("/api/quit", auth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
		go u.quit()
	}))

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	return "http://" + ln.Addr().String() + "/", nil
}
