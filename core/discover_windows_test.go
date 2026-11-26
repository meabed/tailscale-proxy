//go:build windows

package core

import "testing"

func TestParseNetstat(t *testing.T) {
	out := "  Proto  Local Address    Foreign Address   State        PID\n" +
		"  TCP    0.0.0.0:3000     0.0.0.0:0         LISTENING    1234\n" +
		"  TCP    [::]:4983        [::]:0            LISTENING    1234\n" +
		"  TCP    0.0.0.0:9000     0.0.0.0:0         LISTENING    5\n" + // out of range
		"  TCP    0.0.0.0:3000     1.2.3.4:55        ESTABLISHED  77\n" // not listening
	ls := parseNetstat(out, portRange{Lo: 3000, Hi: 5000})
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

// A PID reused by another program between scans must get the new program's
// name, never a remembered one.
func TestListeners_reusedPIDGetsCurrentName(t *testing.T) {
	r := &countingRunner{
		netstat:  "  TCP    0.0.0.0:3000     0.0.0.0:0         LISTENING    1234\n",
		tasklist: "\"node.exe\",\"1234\",\"Console\",\"1\",\"50,000 K\"\n",
		calls:    map[string]int{},
	}
	d := newDiscoverer(r)
	if ls, err := d.listeners(portRange{3000, 5000}); err != nil || len(ls) != 1 || ls[0].Comm != "node.exe" {
		t.Fatalf("first scan: %+v, %v", ls, err)
	}
	r.tasklist = "\"python.exe\",\"1234\",\"Console\",\"1\",\"40,000 K\"\n" // PID 1234 reused
	if ls, _ := d.listeners(portRange{3000, 5000}); len(ls) != 1 || ls[0].Comm != "python.exe" {
		t.Fatalf("reused PID kept the old name: %+v", ls)
	}
}
