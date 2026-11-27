package core

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRedirectHandler_httpGoesToHTTPS(t *testing.T) {
	h := redirectHandler()
	for _, tc := range []struct{ host, target string }{
		{"api.localhost", "/x?y=1"},
		{"api.localhost:80", "/x?y=1"},
		{"web.acme.localhost", "/"},
	} {
		req := httptest.NewRequest("GET", "http://"+tc.host+tc.target, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		host, _, err := net.SplitHostPort(tc.host)
		if err != nil {
			host = tc.host
		}
		if want := "https://" + host + tc.target; rec.Code != http.StatusPermanentRedirect || rec.Header().Get("Location") != want {
			t.Errorf("%s%s: %d %q, want 308 %q", tc.host, tc.target, rec.Code, rec.Header().Get("Location"), want)
		}
	}
	// The bare localhost goes to the dashboard.
	req := httptest.NewRequest("GET", "http://localhost/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusPermanentRedirect || rec.Header().Get("Location") != "https://tsp.localhost/" {
		t.Errorf("localhost: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// A host outside .localhost is not redirected to a name with no certificate.
	req = httptest.NewRequest("GET", "http://127.0.0.1/", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound || rec.Header().Get("Location") != "" {
		t.Errorf("127.0.0.1: %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestValidateName_reservedNames(t *testing.T) {
	for _, name := range []string{dashboardName, healthName} {
		err := validateName(name)
		if err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Errorf("validateName(%q) = %v, want reserved", name, err)
		}
	}
	if _, err := resolveRun([]string{"tsp", "--", "node", "server.js"}, t.TempDir(), func(string) string { return "" }); err == nil {
		t.Error("tsp run tsp was accepted")
	}
	if err := validateName("tsp.app"); err != nil {
		t.Errorf("tsp.app: %v", err)
	}
}

func get(h http.Handler, host, path, accept string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	req.Host = host
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestNotFound_textForClientsHTMLForBrowsers(t *testing.T) {
	reg := testRegistry(t)
	running(t, reg, localRoute{Name: "web", Port: 4100, PID: 1, Dir: "/src/<web>"})
	h := newLocalHandler(t.TempDir(), reg, nil, newProxyMetrics(), "errors")

	text := get(h, "nope.localhost", "/", "")
	body := text.Body.String()
	for _, want := range []string{
		"tsp: nothing runs at https://nope.localhost",
		"https://web.localhost  →  127.0.0.1:4100",
		`put .tsp.json in the project: { "name": "nope" }`,
		"tsp run nope -- 'vite --port $PORT --host $HOST'",
		"tsp run nope -- 'uvicorn main:app --reload --port $PORT --host $HOST'",
		"listen on $PORT and $HOST",
		dashboardURL,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("plain 404 lacks %q:\n%s", want, body)
		}
	}
	if text.Code != 404 || !strings.HasPrefix(text.Header().Get("Content-Type"), "text/plain") {
		t.Errorf("plain 404: %d %s", text.Code, text.Header().Get("Content-Type"))
	}

	page := get(h, "nope.localhost", "/", "text/html,application/xhtml+xml")
	html := page.Body.String()
	if page.Code != 404 || !strings.HasPrefix(page.Header().Get("Content-Type"), "text/html") {
		t.Errorf("html 404: %d %s", page.Code, page.Header().Get("Content-Type"))
	}
	if !strings.Contains(html, "Nothing runs at") || !strings.Contains(html, `href="https://web.localhost"`) {
		t.Errorf("html 404 page:\n%s", html)
	}
	if strings.Contains(html, "<web>") || !strings.Contains(html, "&lt;web&gt;") {
		t.Error("a folder name reached the page unescaped")
	}
}

func TestBadGateway_namesTheExpectedPort(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	reg := testRegistry(t)
	running(t, reg, localRoute{Name: "gone", Port: port, PID: 7, Dir: "/src/gone"})
	h := newLocalHandler(t.TempDir(), reg, nil, newProxyMetrics(), "errors")

	text := get(h, "gone.localhost", "/", "")
	if text.Code != 502 || !strings.Contains(text.Body.String(), "PORT=") || !strings.Contains(text.Body.String(), "tsp logs") {
		t.Errorf("plain 502: %d\n%s", text.Code, text.Body.String())
	}
	page := get(h, "gone.localhost", "/", "text/html")
	if page.Code != 502 || !strings.Contains(page.Body.String(), "is not answering") || !strings.HasPrefix(page.Header().Get("Content-Type"), "text/html") {
		t.Errorf("html 502: %d\n%s", page.Code, page.Body.String())
	}
}

func TestDashboard_pageAndStatus(t *testing.T) {
	reg := testRegistry(t)
	running(t, reg, localRoute{Name: "api", Port: 4200, PID: 42, Dir: `/src/"><script>alert(1)</script>`})
	m := newProxyMetrics()
	m.record("api", 200, 0, 10, false)
	h := newLocalHandler(t.TempDir(), reg, nil, m, "errors")

	page := get(h, "tsp.localhost", "/", "text/html")
	html := page.Body.String()
	if page.Code != 200 || !strings.Contains(html, "Local HTTPS names") || !strings.Contains(html, `href="https://api.localhost"`) {
		t.Fatalf("dashboard: %d\n%s", page.Code, html)
	}
	if strings.Contains(html, "<script>alert(1)") {
		t.Error("the dashboard printed a folder name unescaped")
	}

	rec := get(h, "tsp.localhost", "/api/status", "")
	var st dashboardStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("status JSON: %v\n%s", err, rec.Body.String())
	}
	if len(st.Apps) != 1 || st.Apps[0].Name != "api" || st.Apps[0].Requests != 1 || st.Apps[0].Port != 4200 || st.Proxy.Version == "" {
		t.Errorf("status = %+v", st)
	}

	req := httptest.NewRequest("POST", "/api/status", nil)
	req.Host = "tsp.localhost"
	post := httptest.NewRecorder()
	h.ServeHTTP(post, req)
	if post.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d, want 405", post.Code)
	}
}

func TestCommandArgv_oneQuotedCommandRunsInTheShell(t *testing.T) {
	if got := commandArgv([]string{"bun", "src/index.ts"}); strings.Join(got, " ") != "bun src/index.ts" {
		t.Errorf("several words: %q", got)
	}
	if got := commandArgv([]string{"vite"}); len(got) != 1 {
		t.Errorf("one word: %q", got)
	}
	for script, want := range map[string]string{
		"vite --port $PORT --host $HOST": "exec vite --port $PORT --host $HOST",
		"npm run build && npm start":     "npm run build && npm start",
		"FOO=1 node server.js":           "FOO=1 node server.js",
		"exit 3":                         "exit 3",
		"cd web; vite --port $PORT":      "cd web; vite --port $PORT",
	} {
		if got := execScript(script); got != want {
			t.Errorf("execScript(%q) = %q, want %q", script, got, want)
		}
	}
}
