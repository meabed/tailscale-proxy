//go:build windows

package core

import "testing"

func TestParseNetstat(t *testing.T) {
	out := "  Proto  Local Address    Foreign Address   State        PID\n" +
		"  TCP    0.0.0.0:3000     0.0.0.0:0         LISTENING    1234\n" +
		"  TCP    [::]:4983        [::]:0            LISTENING    1234\n" +
		"  TCP    0.0.0.0:9000     0.0.0.0:0         LISTENING    5\n" + // out of range
		"  TCP    0.0.0.0:3000     1.2.3.4:55        ESTABLISHED  77\n" // not listening
	ls := parseNetstat(out, PortRange{Lo: 3000, Hi: 5000})
	if len(ls) != 2 {
		t.Fatalf("got %d: %+v", len(ls), ls)
	}
}

func TestParseTasklist(t *testing.T) {
	out := "\"node.exe\",\"1234\",\"Console\",\"1\",\"50,000 K\"\n" +
		"\"bun.exe\",\"5678\",\"Console\",\"1\",\"60,000 K\"\n"
	m := parseTasklist(out)
	if m[1234] != "node.exe" || m[5678] != "bun.exe" {
		t.Errorf("got %v", m)
	}
}

// countingRunner answers netstat/tasklist with canned output and counts calls.
type countingRunner struct {
	netstat, tasklist string
	calls             map[string]int
}

func (r *countingRunner) Run(name string, _ ...string) (string, string, error) {
	r.calls[name]++
	if name == "netstat" {
		return r.netstat, "", nil
	}
	return r.tasklist, "", nil
}

// tasklist runs once for new PIDs, not on every re-scan; a new PID triggers it again.
func TestListeners_cachesTasklist(t *testing.T) {
	r := &countingRunner{
		netstat:  "  TCP    0.0.0.0:3000     0.0.0.0:0         LISTENING    1234\n",
		tasklist: "\"node.exe\",\"1234\",\"Console\",\"1\",\"50,000 K\"\n\"bun.exe\",\"5678\",\"Console\",\"1\",\"60,000 K\"\n",
		calls:    map[string]int{},
	}
	d := newDiscoverer(r)
	for i := 0; i < 3; i++ {
		ls, err := d.listeners(PortRange{3000, 5000})
		if err != nil || len(ls) != 1 || ls[0].Comm != "node.exe" {
			t.Fatalf("scan %d: %+v, %v", i, ls, err)
		}
	}
	if r.calls["tasklist"] != 1 {
		t.Fatalf("tasklist calls = %d, want 1", r.calls["tasklist"])
	}
	r.netstat += "  TCP    0.0.0.0:4000     0.0.0.0:0         LISTENING    5678\n"
	ls, _ := d.listeners(PortRange{3000, 5000})
	if len(ls) != 2 || ls[1].Comm != "bun.exe" || r.calls["tasklist"] != 2 {
		t.Fatalf("new PID not resolved: %+v, calls %v", ls, r.calls)
	}
}
