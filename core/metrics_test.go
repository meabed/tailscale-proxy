package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProxyMetrics_recordAndPercentiles(t *testing.T) {
	m := newProxyMetrics()
	for i := 0; i < 90; i++ {
		m.record("api", 200, 4*time.Millisecond, 100, false)
	}
	for i := 0; i < 9; i++ {
		m.record("api", 503, 80*time.Millisecond, 0, false)
	}
	m.record("api", 502, 3*time.Second, 0, true)
	r := m.snapshot().Routes["api"]
	if r.Requests != 100 || r.S2xx != 90 || r.S5xx != 10 || r.Errors != 1 || r.Bytes != 9000 {
		t.Fatalf("counts = %+v", r)
	}
	if p50, p95, p99 := r.percentile(0.5), r.percentile(0.95), r.percentile(0.99); p50 != 5*time.Millisecond || p95 != 100*time.Millisecond || p99 != 100*time.Millisecond {
		t.Errorf("p50=%s p95=%s p99=%s", p50, p95, p99)
	}
	if d := r.minus(r); d.Requests != 0 || d.Latency[2] != 0 {
		t.Errorf("a snapshot minus itself = %+v", d)
	}
}

func TestMeteredWriter_countsAndKeepsTheWriterReachable(t *testing.T) {
	rec := httptest.NewRecorder()
	mw := newMeteredWriter(rec)
	_, _ = mw.Write([]byte("hello"))
	status, _ := mw.result()
	if status != http.StatusOK || mw.bytes != 5 {
		t.Errorf("status %d bytes %d", status, mw.bytes)
	}
	if http.NewResponseController(mw).Flush() != nil {
		t.Error("flushing through the metered writer failed (streams would stall)")
	}
}

func TestHistory_writesDeltasAndSummarizes(t *testing.T) {
	dir := t.TempDir()
	saved := historyEvery
	historyEvery = 20 * time.Millisecond
	t.Cleanup(func() { historyEvery = saved })
	m := newProxyMetrics()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { historyLoop(ctx, dir, "local", m); close(done) }()
	m.record("api", 200, 3*time.Millisecond, 10, false)
	m.record("web", 500, 30*time.Millisecond, 10, false)
	time.Sleep(80 * time.Millisecond)
	m.record("api", 200, 3*time.Millisecond, 10, false)
	time.Sleep(80 * time.Millisecond)
	cancel()
	<-done
	rows := summarize(readHistory(dir, time.Now().Add(-time.Hour)), "local")
	if len(rows) != 2 || rows[0].Name != "api" || rows[0].Requests != 2 || rows[1].Name != "web" || rows[1].S5xx != 1 {
		t.Errorf("summary = %+v", rows)
	}
	if rows := summarize(readHistory(dir, time.Now().Add(-time.Hour)), "start"); len(rows) != 0 {
		t.Errorf("the Tailscale side has rows: %+v", rows)
	}
}

func TestRotatingFile_rotatesAtTheCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "proxy.jsonl")
	w := newRotatingFile(path, 100, 3)
	defer w.Close()
	line := []byte(strings.Repeat("x", 39) + "\n")
	for i := 0; i < 12; i++ {
		if _, err := w.Write(line); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range rotatedPaths(path, 3) {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if fi.Size() > 100 {
			t.Errorf("%s is %d bytes, over the cap", p, fi.Size())
		}
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Error("kept more than three files")
	}
}

func TestPrintLogLine(t *testing.T) {
	var b bytes.Buffer
	raw, _ := json.Marshal(map[string]any{"time": "2026-11-27T13:20:01Z", "level": "WARN", "msg": "release rolled back", "role": "supervisor", "release": "1.15.0"})
	printLogLine(&b, raw, false)
	if got := b.String(); !strings.Contains(got, "WARN   supervisor  release rolled back  release=1.15.0") {
		t.Errorf("line = %q", got)
	}
}

func TestParseSince(t *testing.T) {
	for in, want := range map[string]time.Duration{"30m": 30 * time.Minute, "6h": 6 * time.Hour, "7d": 7 * 24 * time.Hour} {
		if got, err := parseSince(in); err != nil || got != want {
			t.Errorf("parseSince(%q) = %s, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "0d", "-1h", "soon"} {
		if _, err := parseSince(bad); err == nil {
			t.Errorf("parseSince(%q) accepted", bad)
		}
	}
}

func TestCmdStats_notRunning(t *testing.T) {
	isolate(t)
	out := captureStdout(t, func() {
		if code := cmdStats(nil); code != 0 {
			t.Errorf("code = %d", code)
		}
	})
	if !strings.Contains(out, "✗ not running → tsp service install") || !strings.Contains(out, "No requests in the last 24h") {
		t.Errorf("output:\n%s", out)
	}
}

func TestCmdLogs_noEventsYet(t *testing.T) {
	isolate(t)
	out := captureStdout(t, func() { cmdLogs(nil) })
	if !strings.Contains(out, "No events yet") {
		t.Errorf("output:\n%s", out)
	}
}
