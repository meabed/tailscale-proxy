package core

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

// `tsp stats` and `tsp logs`: how the proxies run, from their live counters, the
// per-minute history in ~/.tsp/logs/stats.jsonl and the event log proxy.jsonl.

func statsUsage() {
	fmt.Print(`tsp stats — how the proxies run: health, and requests per name

Usage:
  tsp stats                 the local HTTPS proxy now, and requests per name in the last 24h
  tsp stats --since 1h      a shorter (or longer: 7d) window
  tsp stats --json          the same, for scripts

Requests come from one-minute summaries in ~/.tsp/logs/stats.jsonl, kept across
restarts and updates; p50/p95/p99 are times to the first byte of the answer.
`)
}

// nameSummary is one name's requests over a window.
type nameSummary struct {
	Name     string  `json:"name"`
	Requests uint64  `json:"requests"`
	Errors   uint64  `json:"upstreamErrors"`
	S4xx     uint64  `json:"s4xx"`
	S5xx     uint64  `json:"s5xx"`
	Bytes    uint64  `json:"bytes"`
	P50ms    float64 `json:"p50ms"`
	P95ms    float64 `json:"p95ms"`
	P99ms    float64 `json:"p99ms"`
}

// summarize adds up the history lines of one proxy kind per name, busiest first.
func summarize(lines []historyLine, proxy string) []nameSummary {
	sums := map[string]*routeSnapshot{}
	for _, l := range lines {
		if l.Proxy != proxy {
			continue
		}
		for name, r := range l.Routes {
			if sums[name] == nil {
				sums[name] = &routeSnapshot{}
			}
			sums[name].add(r)
		}
	}
	out := make([]nameSummary, 0, len(sums))
	for name, r := range sums {
		ms := func(q float64) float64 { return float64(r.percentile(q)) / float64(time.Millisecond) }
		out = append(out, nameSummary{Name: name, Requests: r.Requests, Errors: r.Errors, S4xx: r.S4xx, S5xx: r.S5xx,
			Bytes: r.Bytes, P50ms: ms(0.50), P95ms: ms(0.95), P99ms: ms(0.99)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Requests != out[j].Requests {
			return out[i].Requests > out[j].Requests
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// parseSince reads a window: Go durations plus days ("7d").
func parseSince(s string) (time.Duration, error) {
	if d, ok := strings.CutSuffix(s, "d"); ok {
		var n int
		if _, err := fmt.Sscanf(d, "%d", &n); err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid --since %q: use 30m, 6h or 7d", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid --since %q: use 30m, 6h or 7d", s)
	}
	return d, nil
}

func cmdStats(argv []string) int {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	fs.Usage = statsUsage
	asJSON := fs.Bool("json", false, "machine-readable output")
	sinceFlag := fs.String("since", "24h", "window for the request table")
	if err := fs.Parse(argv); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	since, err := parseSince(*sinceFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	dir, err := stateDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	st, running := runningProxy(dir)
	var live *metricsSnapshot
	if running {
		if s, err := fetchLiveStats(dir, st.Worker); err == nil {
			live = &s
		}
	}
	history := readHistory(dir, time.Now().Add(-since))
	local, start := summarize(history, "local"), summarize(history, "start")

	if *asJSON {
		out := map[string]any{"since": time.Now().Add(-since).UTC(), "local": local, "start": start, "updates": loadUpdateState(dir)}
		if running {
			out["proxy"] = st
		}
		if live != nil {
			out["process"] = live.Process
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		return 0
	}

	fmt.Println("Local HTTPS proxy (https://*.localhost)")
	if !running {
		fmt.Println("  ✗ not running → tsp service install (tsp doctor checks the rest)")
	} else {
		fmt.Printf("  serving   tsp %s, pid %d", st.Version, st.Worker)
		if st.Mode != "" {
			fmt.Printf(" (%s)", st.Mode)
		}
		if live != nil {
			fmt.Printf(", up %s", (time.Duration(live.Process.Uptime) * time.Second).String())
		}
		if st.Launcher != "" && !st.Started.IsZero() {
			fmt.Printf("; boot service %s, pid %d, since %s", st.Launcher, st.PID, st.Started.Local().Format("Jan 2 15:04"))
		}
		fmt.Println()
		if live != nil {
			p := live.Process
			fmt.Printf("  process   %.0f MB, %d goroutines, %d open connections, CPU %.1fs, %d certificates issued\n",
				p.MemoryMB, p.Goroutines, p.Conns, p.CPUSeconds, p.Certs)
		}
		fmt.Printf("  changes   %d live updates, %d set aside, %d crashes\n", st.Swaps, st.Rollbacks, st.Crashes)
	}
	fmt.Printf("  updates   %s\n", autoUpdateCheck(dir, time.Now()).Name)
	printSummaries(fmt.Sprintf("Requests per name, last %s", *sinceFlag), local)
	if len(start) > 0 {
		printSummaries(fmt.Sprintf("Online links (tsp start), last %s", *sinceFlag), start)
	}
	if len(local) == 0 && len(start) == 0 {
		fmt.Printf("\nNo requests in the last %s (summaries are written each minute while names get traffic).\n", *sinceFlag)
	}
	return 0
}

func printSummaries(title string, rows []nameSummary) {
	if len(rows) == 0 {
		return
	}
	fmt.Printf("\n%s\n", title)
	w := 4
	for _, r := range rows {
		w = max(w, len(r.Name))
	}
	fmt.Printf("  %-*s  %9s  %6s  %5s  %5s  %7s  %7s  %7s\n", w, "NAME", "REQUESTS", "FAILED", "4XX", "5XX", "P50", "P95", "P99")
	for _, r := range rows {
		fmt.Printf("  %-*s  %9d  %6d  %5d  %5d  %7s  %7s  %7s\n", w, r.Name, r.Requests, r.Errors, r.S4xx, r.S5xx,
			fmtMs(r.P50ms), fmtMs(r.P95ms), fmtMs(r.P99ms))
	}
}

func fmtMs(ms float64) string {
	switch {
	case ms <= 0:
		return "-"
	case ms >= 1000:
		return fmt.Sprintf("%.1fs", ms/1000)
	}
	return fmt.Sprintf("%.0fms", ms)
}

func logsUsage() {
	fmt.Print(`tsp logs — the local HTTPS proxy's events: starts, updates, rollbacks, failed or slow requests

Usage:
  tsp logs            the last 40 events
  tsp logs -n 200     the last 200
  tsp logs -f         keep following new events (Ctrl-C stops)
  tsp logs --json     the raw JSON lines

From ~/.tsp/logs/proxy.jsonl (rotated at 5 MB, three files kept). Every request is
logged with tsp configure --local-request-log=all (default: errors).
`)
}

func cmdLogs(argv []string) int {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.Usage = logsUsage
	follow := fs.Bool("f", false, "follow")
	n := fs.Int("n", 40, "events to show")
	asJSON := fs.Bool("json", false, "raw JSON lines")
	if err := fs.Parse(argv); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	dir, err := stateDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	path := proxyLogPath(dir)
	var lines [][]byte
	for _, p := range rotatedPaths(path, logKeep) {
		if data, err := os.ReadFile(p); err == nil {
			lines = append(lines, splitLines(data)...)
		}
	}
	if len(lines) == 0 && !*follow {
		fmt.Printf("No events yet in %s: the local HTTPS proxy writes them once it runs (tsp service install).\n", path)
		return 0
	}
	for _, l := range lines[max(0, len(lines)-*n):] {
		printLogLine(os.Stdout, l, *asJSON)
	}
	if !*follow {
		return 0
	}
	return followLog(path, *asJSON)
}

// followLog prints lines appended to path, reopening it when it is rotated.
func followLog(path string, asJSON bool) int {
	f, err := os.Open(path)
	for err != nil {
		time.Sleep(time.Second)
		f, err = os.Open(path)
	}
	_, _ = f.Seek(0, io.SeekEnd)
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 && err == nil {
			printLogLine(os.Stdout, line[:len(line)-1], asJSON)
			continue
		}
		time.Sleep(500 * time.Millisecond)
		if cur, statErr := os.Stat(path); statErr == nil && !sameInode(f, cur) {
			_ = f.Close()
			if f, err = os.Open(path); err != nil {
				return 1
			}
			r = bufio.NewReader(f)
		}
	}
}

// printLogLine prints one event as "Jan 2 15:04:05  WARN  worker  msg  k=v …".
func printLogLine(w io.Writer, raw []byte, asJSON bool) {
	if asJSON {
		fmt.Fprintln(w, string(raw))
		return
	}
	var ev map[string]any
	if json.Unmarshal(raw, &ev) != nil {
		fmt.Fprintln(w, string(raw))
		return
	}
	ts, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(ev["time"]))
	stamp := ts.Local().Format("Jan 2 15:04:05")
	level, _ := ev["level"].(string)
	role, _ := ev["role"].(string)
	msg, _ := ev["msg"].(string)
	keys := make([]string, 0, len(ev))
	for k := range ev {
		switch k {
		case "time", "level", "msg", "role":
		default:
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "  %s=%v", k, ev[k])
	}
	fmt.Fprintf(w, "%s  %-5s  %-10s  %s%s\n", stamp, level, role, msg, b.String())
}
