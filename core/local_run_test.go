package core

import (
	"bufio"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func writePkg(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if body != "" {
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestResolveRun_explicit(t *testing.T) {
	spec, err := resolveRun([]string{"api.acme", "bun", "src/index.ts"}, t.TempDir(), env(nil))
	if err != nil || spec.name != "api.acme" || strings.Join(spec.argv, " ") != "bun src/index.ts" {
		t.Fatalf("got %+v, %v", spec, err)
	}
	spec, _ = resolveRun([]string{"api.acme", "--", "bun", "--hot"}, t.TempDir(), env(nil))
	if strings.Join(spec.argv, " ") != "bun --hot" {
		t.Errorf("with -- the command is %v", spec.argv)
	}
	for _, args := range [][]string{{"api.acme"}, {"api.acme", "--"}} {
		if _, err := resolveRun(args, t.TempDir(), env(nil)); err == nil {
			t.Errorf("%v without a command was accepted", args)
		}
	}
	if _, err := resolveRun([]string{"Bad_Name", "x"}, t.TempDir(), env(nil)); err == nil {
		t.Error("an invalid name was accepted")
	}
}

func TestResolveRun_packageJSON(t *testing.T) {
	pkg := `{"scripts":{"dev":"tsp run","dev:app":"bun --hot src/index.ts"},"tsp":{"name":"api.acme","script":"dev:app"}}`
	spec, err := resolveRun(nil, writePkg(t, pkg), env(map[string]string{"npm_execpath": "/opt/bun"}))
	if err != nil || spec.name != "api.acme" || strings.Join(spec.argv, " ") != "/opt/bun run dev:app" {
		t.Fatalf("got %+v, %v", spec, err)
	}
	spec, _ = resolveRun(nil, writePkg(t, pkg), env(nil))
	if spec.argv[0] != "npm" {
		t.Errorf("without npm_execpath the script runs with %q, want npm", spec.argv[0])
	}

	for body, want := range map[string]string{
		"":                       "name the app and its dev command",
		"{":                      "package.json: unexpected end of JSON input",
		`{"scripts":{}}`:         "name the app and its dev command",
		`{"tsp":{"name":"a"}}`:   "name the app and its dev command",
		`{"tsp":{"script":"x"}}`: "name the app and its dev command",
		`{"scripts":{},"tsp":{"name":"a","script":"dev:app"}}`:                `no script "dev:app"`,
		`{"scripts":{"dev:app":"x"},"tsp":{"name":"A_B","script":"dev:app"}}`: "invalid name",
	} {
		if _, err := resolveRun(nil, writePkg(t, body), env(nil)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("package.json %q: err = %v, want %q", body, err, want)
		}
	}
}

func TestRunEnv(t *testing.T) {
	got := runEnv([]string{"A=1"}, 4123, "/ca.pem", env(nil))
	want := []string{"A=1", "PORT=4123", "HOST=127.0.0.1", "NODE_EXTRA_CA_CERTS=/ca.pem"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("runEnv = %v, want %v", got, want)
	}
	kept := runEnv(nil, 4123, "/ca.pem", env(map[string]string{"NODE_EXTRA_CA_CERTS": "/mine.pem"}))
	if strings.Contains(strings.Join(kept, " "), "/ca.pem") {
		t.Error("runEnv replaced an existing NODE_EXTRA_CA_CERTS")
	}
	if noCA := runEnv(nil, 4123, "", env(nil)); len(noCA) != 2 {
		t.Errorf("runEnv without a CA = %v", noCA)
	}
}

// shell returns argv running script in the platform shell.
func shell(script string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", script}
	}
	return []string{"sh", "-c", script}
}

func TestRunChild_exitCodes(t *testing.T) {
	if code := runChild(shell("exit 0"), os.Environ()); code != 0 {
		t.Errorf("exit 0 → %d", code)
	}
	if code := runChild(shell("exit 3"), os.Environ()); code != 3 {
		t.Errorf("exit 3 → %d", code)
	}
	if code := runChild([]string{filepath.Join(t.TempDir(), "missing")}, os.Environ()); code != 127 {
		t.Errorf("missing command → %d, want 127", code)
	}
}

// isolate points HOME at a temp dir and runs in a temp cwd.
func isolate(t *testing.T) (home, cwd string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cwd = t.TempDir()
	t.Chdir(cwd)
	orig := stdinIsTerminal // never set up the machine's real service from a test
	stdinIsTerminal = func() bool { return false }
	t.Cleanup(func() { stdinIsTerminal = orig })
	return home, cwd
}

func TestCmdRun_registersWhileRunning(t *testing.T) {
	home, cwd := isolate(t)
	t.Setenv("NODE_EXTRA_CA_CERTS", "")
	out := filepath.Join(cwd, "seen")
	script := "echo $PORT $HOST $NODE_EXTRA_CA_CERTS > seen; ls " + filepath.Join(home, ".tsp", "routes") + " >> seen"
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell")
	}
	if code := cmdRun(append([]string{"api.acme", "--"}, shell(script)...)); code != 0 {
		t.Fatalf("cmdRun = %d", code)
	}
	data, _ := os.ReadFile(out)
	fields := strings.Fields(string(data))
	if len(fields) != 5 {
		t.Fatalf("the app saw %q", data)
	}
	port, _ := strconv.Atoi(fields[0])
	if port < localPortMin || port > localPortMax || fields[1] != "127.0.0.1" {
		t.Errorf("PORT=%s HOST=%s", fields[0], fields[1])
	}
	if fields[2] != filepath.Join(home, ".tsp", "ca", "ca.pem") {
		t.Errorf("NODE_EXTRA_CA_CERTS=%s", fields[2])
	}
	if fields[3] != "api.acme.json" || fields[4] != "api.acme.lock" {
		t.Errorf("while running, the routes dir held %v", fields[3:])
	}
	if routes := newLocalRegistry(filepath.Join(home, ".tsp", "routes")).list(); len(routes) != 0 {
		t.Errorf("routes left after the app exited: %+v", routes)
	}
}

func TestCmdRun_exitCodeAndTSP0(t *testing.T) {
	isolate(t)
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell")
	}
	if code := cmdRun(append([]string{"api", "--"}, shell("exit 5")...)); code != 5 {
		t.Errorf("exit code passed through = %d, want 5", code)
	}
	t.Setenv("TSP", "0")
	t.Setenv("PORT", "")
	if code := cmdRun(append([]string{"api", "--"}, shell(`test -z "$PORT"`)...)); code != 0 {
		t.Error("TSP=0 still set PORT")
	}
}

// TestHelperHoldName is not a test: started by TestCmdRun_nameTakenAndForce as a
// stand-in `tsp run` in another process, it claims a name and holds it until SIGTERM.
func TestHelperHoldName(t *testing.T) {
	dir := os.Getenv("TSP_TEST_HOLD")
	if dir == "" {
		t.Skip("helper process")
	}
	reg := newLocalRegistry(dir)
	release, ok, err := reg.claim("api")
	if err != nil || !ok {
		os.Exit(3)
	}
	_ = reg.add(localRoute{Name: "api", Port: 4555, PID: os.Getpid(), Dir: "/elsewhere"})
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM)
	os.Stdout.WriteString("ready\n")
	<-sigs
	reg.remove("api")
	release()
	os.Exit(0)
}

func TestCmdRun_nameTakenAndForce(t *testing.T) {
	home, _ := isolate(t)
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell and SIGTERM")
	}
	routes := filepath.Join(home, ".tsp", "routes")
	_ = os.MkdirAll(routes, 0o755)
	holder := exec.Command(os.Args[0], "-test.run=^TestHelperHoldName$")
	holder.Env = append(os.Environ(), "TSP_TEST_HOLD="+routes)
	out, _ := holder.StdoutPipe()
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Process.Kill() })
	if line, _ := bufio.NewReader(out).ReadString('\n'); line != "ready\n" {
		t.Fatalf("helper said %q", line)
	}

	if code := cmdRun(append([]string{"api", "--"}, shell("exit 0")...)); code != 1 {
		t.Errorf("a taken name = %d, want 1", code)
	}
	if code := cmdRun(append([]string{"--force", "api", "--"}, shell("exit 0")...)); code != 0 {
		t.Errorf("--force = %d, want 0", code)
	}
	if err := holder.Wait(); err != nil {
		t.Errorf("the previous run did not exit cleanly on SIGTERM: %v", err)
	}
	if _, err := os.Stat(filepath.Join(routes, "api.json")); !os.IsNotExist(err) {
		t.Error("api.json left after the taking-over run ended")
	}
}

func TestCmdRun_argumentShapes(t *testing.T) {
	isolate(t)
	for _, argv := range [][]string{
		{"--", "bun", "dev"},          // no name
		{"api", "bun", "dev"},         // command without --
		{"api", "--force", "--", "x"}, // flag after the name
		{"api"},                       // name without a command
	} {
		if code := cmdRun(argv); code != 2 {
			t.Errorf("tsp run %v = %d, want 2", argv, code)
		}
	}
}

func TestCmdRun_usageErrors(t *testing.T) {
	isolate(t)
	if code := cmdRun([]string{"-h"}); code != 0 {
		t.Errorf("-h = %d", code)
	}
	if code := cmdRun([]string{"--nope"}); code != 2 {
		t.Errorf("unknown flag = %d", code)
	}
	if code := cmdRun(nil); code != 2 {
		t.Errorf("no package.json = %d, want 2", code)
	}
}
