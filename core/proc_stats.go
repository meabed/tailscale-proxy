package core

import (
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// What an app costs right now: CPU, memory and processes of the one it runs and
// every process that one started (npm → node → esbuild), from one `ps` of all
// processes (psTable). CPU is measured between two looks (the dashboard looks
// every few seconds); the first look uses ps's own figure.

// psRow is one process as ps shows it.
type psRow struct {
	PPID    int
	CPUTime time.Duration // CPU used since it started
	PCPU    float64       // ps's %CPU (macOS: recent; Linux: since it started)
	RSSKB   int64
	Elapsed time.Duration // since it started
}

// procStats is a process with the ones it started.
type procStats struct {
	CPU       float64 `json:"cpu"`      // percent of one core
	MemoryMB  float64 `json:"memoryMB"` // resident memory
	Procs     int     `json:"procs"`
	UpSeconds float64 `json:"upSeconds"` // the process itself
}

func (s procStats) CPUText() string { return strconv.FormatFloat(s.CPU, 'f', 1, 64) + "%" }

func (s procStats) MemoryText() string {
	if s.MemoryMB >= 1024 {
		return strconv.FormatFloat(s.MemoryMB/1024, 'f', 1, 64) + " GB"
	}
	return strconv.FormatFloat(s.MemoryMB, 'f', 0, 64) + " MB"
}

// summary is "3.2% CPU, 182 MB, 4 processes".
func (s procStats) summary() string {
	procs := "1 process"
	if s.Procs != 1 {
		procs = strconv.Itoa(s.Procs) + " processes"
	}
	return s.CPUText() + " CPU, " + s.MemoryText() + ", " + procs
}

// descendants are every process pid started, and the ones those started.
func descendants(table map[int]psRow, pid int) []int {
	children := map[int][]int{}
	for p, row := range table {
		children[row.PPID] = append(children[row.PPID], p)
	}
	var out []int
	for queue := children[pid]; len(queue) > 0; queue = queue[1:] {
		if p := queue[0]; p != pid && !slices.Contains(out, p) {
			out = append(out, p)
			queue = append(queue, children[p]...)
		}
	}
	slices.Sort(out)
	return out
}

// cpuSampler remembers each process's CPU time from the last look, so the next
// one reports the CPU it used in between.
type cpuSampler struct {
	mu   sync.Mutex
	last map[int]cpuSample
}

type cpuSample struct {
	cpu time.Duration
	at  time.Time
}

// stats measures each root with the processes it started; roots ps does not
// show (gone, or Windows) are left out.
func (s *cpuSampler) stats(table map[int]psRow, roots []int) map[int]procStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.last == nil {
		s.last = map[int]cpuSample{}
	}
	next := map[int]cpuSample{}
	out := map[int]procStats{}
	for _, root := range roots {
		row, ok := table[root]
		if !ok {
			continue
		}
		st := procStats{UpSeconds: row.Elapsed.Seconds()}
		for _, pid := range append([]int{root}, descendants(table, root)...) {
			r := table[pid]
			cpu := r.PCPU
			if prev, ok := s.last[pid]; ok && now.Sub(prev.at) >= 500*time.Millisecond && r.CPUTime >= prev.cpu {
				cpu = 100 * float64(r.CPUTime-prev.cpu) / float64(now.Sub(prev.at))
			}
			next[pid] = cpuSample{r.CPUTime, now}
			st.CPU += cpu
			st.MemoryMB += float64(r.RSSKB) / 1024
			st.Procs++
		}
		st.CPU = math.Round(st.CPU*10) / 10
		st.MemoryMB = math.Round(st.MemoryMB*10) / 10
		out[root] = st
	}
	for pid, smp := range s.last { // the other callers' processes stay, until a minute unseen
		if now.Sub(smp.at) > time.Minute {
			delete(s.last, pid)
		}
	}
	maps.Copy(s.last, next)
	return out
}

// parsePSDuration reads ps's TIME and ELAPSED: [[dd-]hh:]mm:ss[.cc].
func parsePSDuration(v string) time.Duration {
	var days float64
	if d, rest, ok := strings.Cut(v, "-"); ok {
		days, _ = strconv.ParseFloat(d, 64)
		v = rest
	}
	var secs float64
	for _, part := range strings.Split(v, ":") {
		n, err := strconv.ParseFloat(part, 64)
		if err != nil {
			return 0
		}
		secs = secs*60 + n
	}
	return time.Duration((days*86400 + secs) * float64(time.Second))
}

// parsePSTable reads `ps -A -o pid=,ppid=,time=,pcpu=,rss=,etime=`.
func parsePSTable(out string) map[int]psRow {
	table := map[int]psRow{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 6 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			continue
		}
		pcpu, _ := strconv.ParseFloat(f[3], 64)
		rss, _ := strconv.ParseInt(f[4], 10, 64)
		table[pid] = psRow{PPID: ppid, CPUTime: parsePSDuration(f[2]), PCPU: pcpu, RSSKB: rss, Elapsed: parsePSDuration(f[5])}
	}
	return table
}
