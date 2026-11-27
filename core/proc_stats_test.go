package core

import (
	"slices"
	"testing"
	"time"
)

func TestParsePSDuration_macOSAndLinuxFormats(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"0:01.23":     1230 * time.Millisecond, // macOS TIME
		"01:02:03":    time.Hour + 2*time.Minute + 3*time.Second,
		"2-03:04:05":  2*24*time.Hour + 3*time.Hour + 4*time.Minute + 5*time.Second,
		"00:07":       7 * time.Second,
		"not a time":  0,
		"12:34:56.50": 12*time.Hour + 34*time.Minute + 56500*time.Millisecond,
	} {
		if got := parsePSDuration(in); got != want {
			t.Errorf("parsePSDuration(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestCPUSampler_measuresTheTreeBetweenLooks(t *testing.T) {
	table := parsePSTable(`
  100     1   0:10.00  50.0  102400  1:00:00
  200   100   0:05.00   5.0   51200    59:00
  300   200   0:01.00   1.0   10240    58:00
  400     1   0:00.10   0.0    1024    00:10
`)
	if d := descendants(table, 100); !slices.Equal(d, []int{200, 300}) {
		t.Fatalf("descendants(100) = %v", d)
	}
	s := &cpuSampler{}
	first := s.stats(table, []int{100})[100]
	if first.Procs != 3 || first.CPU != 56 || first.MemoryMB != 160 || first.UpSeconds != 3600 {
		t.Errorf("first look = %+v", first) // ps's own %CPU, summed over the tree
	}
	// One second later the tree used half a core more: 0.5s of CPU time.
	for pid, smp := range s.last {
		smp.at = smp.at.Add(-time.Second)
		s.last[pid] = smp
	}
	row := table[200]
	row.CPUTime += 500 * time.Millisecond
	table[200] = row
	if second := s.stats(table, []int{100})[100]; second.CPU < 49 || second.CPU > 51 {
		t.Errorf("second look CPU = %v, want about 50", second.CPU)
	}
	if got := (procStats{CPU: 3.25, MemoryMB: 1536, Procs: 4}).summary(); got != "3.2% CPU, 1.5 GB, 4 processes" && got != "3.3% CPU, 1.5 GB, 4 processes" {
		t.Errorf("summary = %q", got)
	}
}
