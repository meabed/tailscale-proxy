package core

// shellArgv runs a one-line command with cmd.exe (%PORT% and %HOST% expand there).
func shellArgv(script string) []string { return []string{"cmd", "/C", script} }
