//go:build !windows

package core

// shellArgv runs a one-line command with /bin/sh, as the app's own process when
// it is a simple command (execScript), so signals and exit codes pass straight through.
func shellArgv(script string) []string { return []string{"/bin/sh", "-c", execScript(script)} }
