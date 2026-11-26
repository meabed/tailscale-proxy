//go:build darwin

package core

func platformService() (serviceSystem, bool) {
	return serviceSystem{
		kind:     "launchd",
		unitPath: "/Library/LaunchDaemons/" + serviceLabel + ".plist",
		binPath:  "/Library/PrivilegedHelperTools/" + serviceLabel, // root:wheel, writable by root only
		logPath:  "/var/log/tailscale-proxy.log",
	}, true
}
