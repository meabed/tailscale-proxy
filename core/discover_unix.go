//go:build !windows

package core

import (
	"fmt"
	"strconv"
	"strings"
)

// listeners lists listening TCP sockets in range via lsof, enriching each with
// the full runtime (ps) and working directory (lsof -d cwd). Those two lookups
// run only for processes not already known from the previous scan.
func (d *discoverer) listeners(rng portRange) ([]listener, error) {
	// -b/-w: skip kernel calls that can block (stat/readlink) and their warnings;
	// -iTCP:<range>: only sockets in range, so lsof doesn't format every one.
	out, stderr, err := d.run.Run("lsof", "-nP", "-b", "-w", "-iTCP:"+lsofPorts(rng), "-sTCP:LISTEN", "-Fpcn")
	if err != nil {
		// Distinguish "lsof not installed" from "lsof found nothing" (lsof exits
		// non-zero with empty output when no sockets match — not an error for us).
		// A non-zero exit with output is parsed as-is.
		if strings.Contains(err.Error(), "executable file not found") ||
			strings.Contains(stderr, "not found") {
			return nil, fmt.Errorf("lsof not found — install it (macOS ships it; Linux: apt/dnf install lsof)")
		}
	}
	ls := parseLsofListeners(out, rng)
	d.resolveProcs(ls)
	return ls, nil
}

// lsofPorts renders a range as an lsof port spec ("3000-6000", or "4000").
func lsofPorts(rng portRange) string {
	if rng.Lo == rng.Hi {
		return strconv.Itoa(rng.Lo)
	}
	return strconv.Itoa(rng.Lo) + "-" + strconv.Itoa(rng.Hi)
}

// resolveProcs fills each listener's PsComm, Cwd and Root from the process
// cache, running ps and the cwd lsof once for all PIDs that are new (or reused
// under another command). PIDs absent from ls are evicted.
func (d *discoverer) resolveProcs(ls []listener) {
	var fresh, needPs []string
	seen := map[int]bool{}
	for _, l := range ls {
		if seen[l.PID] {
			continue
		}
		seen[l.PID] = true
		if p, ok := d.procs[l.PID]; ok && p.comm == l.Comm {
			continue
		}
		pid := strconv.Itoa(l.PID)
		fresh = append(fresh, pid)
		// The ps name only classifies processes lsof's command doesn't (runtimeOf).
		if classifyRuntime(l.Comm) == "" {
			needPs = append(needPs, pid)
		}
	}

	// lsof/ps exit non-zero when any PID is gone or unreadable, but still print
	// the rest — use what they print. Only an empty failed run is retried.
	cacheable := true
	var psComm, cwd map[int]string
	if len(needPs) > 0 {
		out, _, err := d.run.Run("ps", "-o", "pid=,comm=", "-p", strings.Join(needPs, ","))
		psComm = parsePsComm(out)
		cacheable = err == nil || strings.TrimSpace(out) != ""
	}
	if len(fresh) > 0 {
		out, _, err := d.run.Run("lsof", "-nP", "-b", "-w", "-a", "-d", "cwd", "-Fpn", "-p", strings.Join(fresh, ","))
		cwd = parseLsofCwd(out)
		cacheable = cacheable && (err == nil || strings.TrimSpace(out) != "")
	}

	next := make(map[int]procInfo, len(seen))
	for i := range ls {
		l := &ls[i]
		p, ok := next[l.PID]
		if !ok {
			p, ok = d.procs[l.PID]
			if !ok || p.comm != l.Comm {
				p = procInfo{comm: l.Comm, psComm: psComm[l.PID], cwd: cwd[l.PID]}
				p.root = projectRootDir(p.cwd)
				ok = cacheable
			}
			if ok {
				next[l.PID] = p
			}
		}
		l.PsComm, l.Cwd, l.Root = p.psComm, p.cwd, p.root
	}
	d.procs = next
}

// parseLsofListeners parses `lsof -Fpcn` output, deduping per (pid,port).
func parseLsofListeners(out string, rng portRange) []listener {
	var res []listener
	seen := map[string]bool{}
	var pid int
	var comm string
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
			comm = ""
		case 'c':
			comm = line[1:]
		case 'n':
			port := portFromAddr(line[1:])
			if port == 0 || !rng.contains(port) {
				continue
			}
			key := strconv.Itoa(pid) + ":" + strconv.Itoa(port)
			if seen[key] {
				continue
			}
			seen[key] = true
			res = append(res, listener{Port: port, PID: pid, Comm: comm})
		}
	}
	return res
}

// parsePsComm parses `ps -o pid=,comm=` output into pid -> executable path.
func parsePsComm(out string) map[int]string {
	m := map[int]string{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		m[pid] = strings.Join(f[1:], " ")
	}
	return m
}

// parseLsofCwd parses `lsof -d cwd -Fpn` output into pid -> cwd.
func parseLsofCwd(out string) map[int]string {
	m := map[int]string{}
	var pid int
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
		case 'n':
			if pid != 0 {
				m[pid] = line[1:]
			}
		}
	}
	return m
}
