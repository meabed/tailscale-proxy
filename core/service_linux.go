//go:build linux

package core

func platformService() (serviceSystem, bool) {
	return serviceSystem{
		kind:     "systemd",
		unitPath: "/etc/systemd/system/tailscale-proxy.service",
		binPath:  "/usr/local/lib/tailscale-proxy/tsp",
	}, true
}
