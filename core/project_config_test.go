package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestProjectConfig_eachFileKind(t *testing.T) {
	for file, body := range map[string]string{
		".tsp.json":    `{"name": "api.acme", "share": "private", "path": "api", "port": 4100}`,
		"package.json": `{"name": "x", "tsp": {"name": "api.acme", "share": "private", "path": "api", "port": 4100}}`,
		"deno.json":    `{"tsp": {"name": "api.acme", "share": "private", "path": "api", "port": 4100}}`,
		"deno.jsonc": `{
  // the app
  "tsp": { "name": "api.acme", /* local */ "share": "private", "path": "api", "port": 4100, },
}`,
		"pyproject.toml": `[project]
name = "x"

[tool.tsp]
name = "api.acme"   # local name
share = "private"
path = 'api'
port = 4_100
`,
		"Cargo.toml": `[package]
name = "x"

[package.metadata.tsp]
name = "api.acme"
share = "private"
path = "api"
port = 4100
`,
		"composer.json": `{"extra": {"tsp": {"name": "api.acme", "share": "private", "path": "api", "port": 4100}}}`,
	} {
		cfg, err := loadProjectConfig(writeFiles(t, map[string]string{file: body}))
		if err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		if cfg.Name != "api.acme" || cfg.Share != "private" || cfg.Path != "api" || cfg.Port != 4100 || !cfg.Enabled || filepath.Base(cfg.File) != file {
			t.Errorf("%s: %+v", file, cfg)
		}
	}
}

func TestProjectConfig_precedenceDefaultsAndErrors(t *testing.T) {
	// .tsp.json wins over package.json.
	cfg, _ := loadProjectConfig(writeFiles(t, map[string]string{".tsp.json": `{"name": "one"}`, "package.json": `{"tsp": {"name": "two"}}`}))
	if cfg.Name != "one" {
		t.Errorf("precedence: %+v", cfg)
	}
	// A package.json without "tsp" is not a config: the next file is read.
	cfg, _ = loadProjectConfig(writeFiles(t, map[string]string{"package.json": `{"name": "x"}`, "pyproject.toml": "[tool.tsp]\nname = \"py\"\n"}))
	if cfg.Name != "py" {
		t.Errorf("fall through: %+v", cfg)
	}
	// No config: enabled, no name.
	if cfg, err := loadProjectConfig(t.TempDir()); err != nil || !cfg.Enabled || cfg.Name != "" || cfg.File != "" {
		t.Errorf("none: %+v %v", cfg, err)
	}
	// share false, enabled false.
	cfg, _ = loadProjectConfig(writeFiles(t, map[string]string{".tsp.json": `{"share": false, "enabled": false}`}))
	if cfg.Share != "off" || cfg.Enabled {
		t.Errorf("off: %+v", cfg)
	}
	for body, want := range map[string]string{
		`{"name": "Bad_Name"}`:  "invalid name",
		`{"name": "tsp"}`:       "reserved",
		`{"share": "everyone"}`: "share",
		`{"path": "A B"}`:       "path",
		`{"port": 70000}`:       "port",
		`{`:                     "unexpected end",
	} {
		_, err := loadProjectConfig(writeFiles(t, map[string]string{".tsp.json": body}))
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), ".tsp.json") {
			t.Errorf("%s: err = %v, want %q naming the file", body, err, want)
		}
	}
	if _, err := loadProjectConfig(writeFiles(t, map[string]string{"pyproject.toml": "[tool.tsp]\nname = [1]\n"})); err == nil {
		t.Error("a TOML array was accepted")
	}
}

func TestProjectConfig_cacheFollowsTheFile(t *testing.T) {
	dir := writeFiles(t, map[string]string{".tsp.json": `{"name": "one"}`})
	if cfg, _ := loadProjectConfig(dir); cfg.Name != "one" {
		t.Fatal(cfg)
	}
	path := filepath.Join(dir, ".tsp.json")
	_ = os.WriteFile(path, []byte(`{"name": "two.app"}`), 0o644)
	_ = os.Chtimes(path, time.Now().Add(time.Second), time.Now().Add(time.Second))
	if cfg, _ := loadProjectConfig(dir); cfg.Name != "two.app" {
		t.Errorf("a changed file was served from the cache: %+v", cfg)
	}
}

func TestBuildServices_projectConfig(t *testing.T) {
	named := writeFiles(t, map[string]string{".tsp.json": `{"name": "api.acme", "port": 4200}`})
	off := writeFiles(t, map[string]string{".tsp.json": `{"enabled": false}`})
	private := writeFiles(t, map[string]string{"package.json": `{"tsp": {"share": false, "path": "docs-site"}}`})
	broken := writeFiles(t, map[string]string{".tsp.json": `{"name": "Bad_Name"}`})
	svcs, _ := buildServices([]listener{
		{Port: 4100, PID: 1, Comm: "node", Cwd: named, Root: named},
		{Port: 4200, PID: 2, Comm: "node", Cwd: named, Root: named},
		{Port: 4300, PID: 3, Comm: "node", Cwd: off, Root: off},
		{Port: 4400, PID: 4, Comm: "python3", Cwd: private, Root: private},
		{Port: 4500, PID: 5, Comm: "node", Cwd: broken, Root: broken},
	}, false, nil)
	got := map[string]Service{}
	for _, s := range svcs {
		got[s.Name] = s
	}
	if s := got["api.acme"]; s.Port != 4200 || s.Slug != "api" {
		t.Errorf("the configured port is the main one, under the name's first label: %+v", got)
	}
	if s := got["api.acme-4100"]; s.Port != 4100 {
		t.Errorf("the other process keeps a suffixed name: %+v", got)
	}
	if s := got[filepath.Base(private)]; s.Share != "off" || s.Slug != "docs-site" {
		t.Errorf("share/path: %+v", got)
	}
	if len(got) != 3 {
		t.Errorf("an enabled:false or broken project was served: %+v", got)
	}
	if p := projectConfigProblems(); len(p) != 1 || !strings.Contains(p[0], "Bad_Name") {
		t.Errorf("problems = %q", p)
	}
}
