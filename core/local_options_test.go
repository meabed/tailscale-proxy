package core

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDefaults_dashboardOnCatchAllOff(t *testing.T) {
	if o := optionsOf(defaultConfig()); !o.Dashboard || o.CatchAll {
		t.Errorf("defaults = %+v, want the dashboard on and the catch-all off", o)
	}
}

func TestAnswers_onlyServedNamesWithoutTheCatchAll(t *testing.T) {
	reg := testRegistry(t)
	running(t, reg, localRoute{Name: "web", Port: 4100, PID: 1, Dir: "/src/web"})
	for _, tc := range []struct {
		o    localOptions
		name string
		want bool
	}{
		{localOptions{Dashboard: true}, "web", true},
		{localOptions{Dashboard: true}, "acme.web", true}, // a served parent
		{localOptions{Dashboard: true}, "nope", false},
		{localOptions{Dashboard: true}, dashboardName, true},
		{localOptions{Dashboard: true}, "", true}, // the bare localhost: the dashboard
		{localOptions{}, dashboardName, false},
		{localOptions{}, "", false},
		{localOptions{}, healthName, true}, // the proxy's own check always answers
		{localOptions{CatchAll: true}, "nope", true},
		{localOptions{CatchAll: true}, dashboardName, true}, // the 404 page, with the dashboard off
	} {
		if got := answers(reg, nil, tc.o, tc.name); got != tc.want {
			t.Errorf("answers(%+v, %q) = %v, want %v", tc.o, tc.name, got, tc.want)
		}
	}
}

func TestLocalHandler_withoutDashboardOrCatchAll(t *testing.T) {
	reg := testRegistry(t)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		_ = http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("app")) }))
	}()
	running(t, reg, localRoute{Name: "web", Port: ln.Addr().(*net.TCPAddr).Port, PID: 1, Dir: "/src/web"})
	none := func() localOptions { return localOptions{} }
	h := newLocalHandler(t.TempDir(), reg, nil, newProxyMetrics(), "errors", none)

	if rec := get(h, "web.localhost", "/", ""); rec.Code != 200 || rec.Body.String() != "app" {
		t.Errorf("a served name: %d %q", rec.Code, rec.Body.String())
	}
	for _, host := range []string{"nope.localhost", "tsp.localhost", "localhost"} {
		rec := get(h, host, "/", "text/html")
		if rec.Code != http.StatusMisdirectedRequest || strings.Contains(rec.Body.String(), "web") {
			t.Errorf("%s: %d\n%s", host, rec.Code, rec.Body.String())
		}
	}
	// The dashboard on, the catch-all off: tsp.localhost answers, an unknown name does not.
	board := newLocalHandler(t.TempDir(), reg, nil, newProxyMetrics(), "errors", func() localOptions { return localOptions{Dashboard: true} })
	if rec := get(board, "tsp.localhost", "/", "text/html"); rec.Code != 200 {
		t.Errorf("tsp.localhost with the dashboard on: %d", rec.Code)
	}
	if rec := get(board, "nope.localhost", "/", "text/html"); rec.Code != http.StatusMisdirectedRequest {
		t.Errorf("an unknown name with the catch-all off: %d", rec.Code)
	}
}

func TestRedirectHandler_closesWhatHTTPSWouldNotAnswer(t *testing.T) {
	served := func(name string) bool { return name == "web" }
	srv := httptest.NewServer(redirectHandler(served, func() localOptions { return localOptions{Dashboard: true} }))
	defer srv.Close()
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	do := func(host string) (*http.Response, error) {
		req, _ := http.NewRequest("GET", srv.URL+"/", nil)
		req.Host = host
		return client.Do(req)
	}
	if res, err := do("web.localhost"); err != nil || res.StatusCode != http.StatusPermanentRedirect {
		t.Errorf("a served name: %v %v", res, err)
	}
	if res, err := do("localhost"); err != nil || res.Header.Get("Location") != dashboardURL+"/" {
		t.Errorf("localhost with the dashboard on: %v %v", res, err)
	}
	for _, host := range []string{"nope.localhost", "rebound.example"} {
		if res, err := do(host); err == nil {
			t.Errorf("%s got an answer: %d", host, res.StatusCode)
		}
	}
}
