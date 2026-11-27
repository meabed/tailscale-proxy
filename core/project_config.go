package core

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A project can say how tsp serves it, in the file its ecosystem already has.
// The first of these in the project root wins:
//
//	.tsp.json                    { "name": "api.acme" }            any language
//	package.json                 "tsp": { … }
//	deno.json / deno.jsonc       "tsp": { … }
//	pyproject.toml               [tool.tsp]
//	Cargo.toml                   [package.metadata.tsp]
//	composer.json                "extra": { "tsp": { … } }
//
// One resolver serves the local names (the proxy's watcher), `tsp start`, `tsp
// list`, `tsp status` and `tsp run`.

// projectConfig is a project's tsp settings; every field is optional.
type projectConfig struct {
	Name    string // https://<name>.localhost (default: the folder's slug)
	Share   string // "" (the user's mode), "off", "private", "public": how `tsp start` exposes it
	Path    string // the Tailscale path segment (default: the name's first label, or the slug)
	Port    int    // the app's listener when the project runs several
	Enabled bool   // false: tsp leaves the project alone
	Script  string // tsp run: a package.json script, or (other files) a shell command
	File    string // where it came from, "" when the project has none
}

// projectConfigFiles in the order they are tried.
var projectConfigFiles = []string{".tsp.json", "package.json", "deno.json", "deno.jsonc", "pyproject.toml", "Cargo.toml", "composer.json"}

// rawProjectConfig is the "tsp" object as written; share is false or a string.
type rawProjectConfig struct {
	Name    string          `json:"name"`
	Share   json.RawMessage `json:"share"`
	Path    string          `json:"path"`
	Port    int             `json:"port"`
	Enabled *bool           `json:"enabled"`
	Script  string          `json:"script"`
}

type configCacheEntry struct {
	mod  time.Time
	size int64
	cfg  projectConfig
	ok   bool // the file has a tsp section
	err  error
}

var configCache sync.Map // path → configCacheEntry

// loadProjectConfig returns root's tsp settings (defaults when it has none). An
// error names the file and what is wrong with it; the caller skips the project.
func loadProjectConfig(root string) (projectConfig, error) {
	if root == "" {
		return projectConfig{Enabled: true}, nil
	}
	for _, name := range projectConfigFiles {
		path := filepath.Join(root, name)
		fi, err := os.Stat(path)
		if err != nil || fi.IsDir() {
			continue
		}
		var e configCacheEntry
		if v, ok := configCache.Load(path); ok && v.(configCacheEntry).mod.Equal(fi.ModTime()) && v.(configCacheEntry).size == fi.Size() {
			e = v.(configCacheEntry)
		} else {
			e = configCacheEntry{mod: fi.ModTime(), size: fi.Size()}
			e.cfg, e.ok, e.err = parseProjectConfigFile(path)
			configCache.Store(path, e)
		}
		if e.err != nil {
			return projectConfig{}, e.err
		}
		if e.ok {
			return e.cfg, nil
		}
	}
	return projectConfig{Enabled: true}, nil
}

// parseProjectConfigFile reads the tsp section of one file (ok=false: it has none).
func parseProjectConfigFile(path string) (projectConfig, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return projectConfig{}, false, err
	}
	var raw *rawProjectConfig
	switch base := filepath.Base(path); base {
	case ".tsp.json":
		raw = &rawProjectConfig{}
		err = json.Unmarshal(data, raw)
	case "package.json", "deno.json", "deno.jsonc":
		var doc struct {
			TSP *rawProjectConfig `json:"tsp"`
		}
		if base == "deno.jsonc" {
			data = stripJSONC(data)
		}
		err = json.Unmarshal(data, &doc)
		raw = doc.TSP
	case "composer.json":
		var doc struct {
			Extra struct {
				TSP *rawProjectConfig `json:"tsp"`
			} `json:"extra"`
		}
		err = json.Unmarshal(data, &doc)
		raw = doc.Extra.TSP
	case "pyproject.toml":
		raw, err = tomlTable(data, "tool.tsp")
	case "Cargo.toml":
		raw, err = tomlTable(data, "package.metadata.tsp")
	}
	if err != nil {
		return projectConfig{}, false, fmt.Errorf("%s: %v", path, err)
	}
	if raw == nil {
		return projectConfig{}, false, nil
	}
	cfg, err := raw.validate()
	if err != nil {
		return projectConfig{}, false, fmt.Errorf("%s: %v", path, err)
	}
	cfg.File = path
	return cfg, true, nil
}

func (r *rawProjectConfig) validate() (projectConfig, error) {
	cfg := projectConfig{Name: r.Name, Path: r.Path, Port: r.Port, Enabled: r.Enabled == nil || *r.Enabled, Script: r.Script}
	if cfg.Name != "" {
		if err := validateName(cfg.Name); err != nil {
			return cfg, err
		}
	}
	if cfg.Path != "" && slugify(cfg.Path) != cfg.Path {
		return cfg, fmt.Errorf("path %q: use lowercase letters, digits and dashes (e.g. %q)", cfg.Path, slugify(cfg.Path))
	}
	if cfg.Port < 0 || cfg.Port > 65535 {
		return cfg, fmt.Errorf("port %d is not a TCP port", cfg.Port)
	}
	switch share := strings.TrimSpace(string(r.Share)); share {
	case "", "null", "true":
	case "false":
		cfg.Share = "off"
	case `"private"`, `"public"`:
		cfg.Share = strings.Trim(share, `"`)
	default:
		return cfg, fmt.Errorf(`share %s: use false, "private" or "public"`, share)
	}
	return cfg, nil
}

// pathSlug is the project's Tailscale path segment: path, else the name's first
// label, else the folder slug.
func (c projectConfig) pathSlug(folderSlug string) string {
	switch {
	case c.Path != "":
		return c.Path
	case c.Name != "":
		first, _, _ := strings.Cut(c.Name, ".")
		return first
	}
	return folderSlug
}

// stripJSONC drops // and /* */ comments and trailing commas outside strings.
func stripJSONC(in []byte) []byte {
	var out bytes.Buffer
	inStr, esc := false, false
	for i := 0; i < len(in); i++ {
		c := in[i]
		switch {
		case inStr:
			out.WriteByte(c)
			if esc {
				esc = false
			} else if c == '\\' {
				esc = true
			} else if c == '"' {
				inStr = false
			}
		case c == '"':
			inStr = true
			out.WriteByte(c)
		case c == '/' && i+1 < len(in) && in[i+1] == '/':
			for i < len(in) && in[i] != '\n' {
				i++
			}
			out.WriteByte('\n')
		case c == '/' && i+1 < len(in) && in[i+1] == '*':
			end := bytes.Index(in[i+2:], []byte("*/"))
			if end < 0 {
				return out.Bytes()
			}
			i += end + 3
		case c == ',':
			j := i + 1
			for j < len(in) && strings.ContainsRune(" \t\r\n", rune(in[j])) {
				j++
			}
			if j < len(in) && (in[j] == '}' || in[j] == ']') {
				continue // a trailing comma
			}
			out.WriteByte(c)
		default:
			out.WriteByte(c)
		}
	}
	return out.Bytes()
}

// tomlTable reads the keys of one [table] of a TOML file: strings (basic and
// literal), booleans and integers, which is all a tsp table holds. nil: the file
// has no such table.
func tomlTable(data []byte, table string) (*rawProjectConfig, error) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	in, found := false, false
	fields := map[string]json.RawMessage{}
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			header := strings.TrimSpace(strings.Trim(stripTOMLComment(line), "[] \t"))
			in = header == table
			found = found || in
			continue
		}
		if !in {
			continue
		}
		key, value, ok := strings.Cut(stripTOMLComment(line), "=")
		if !ok {
			return nil, fmt.Errorf("line %d: want key = value", n)
		}
		key = strings.Trim(strings.TrimSpace(key), `"`)
		v, err := tomlValue(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("line %d (%s): %v", n, key, err)
		}
		fields[key] = v
	}
	if !found {
		return nil, nil
	}
	doc, _ := json.Marshal(fields)
	var raw rawProjectConfig
	if err := json.Unmarshal(doc, &raw); err != nil {
		return nil, err
	}
	return &raw, nil
}

// tomlValue turns a TOML scalar into JSON.
func tomlValue(v string) (json.RawMessage, error) {
	switch {
	case v == "true" || v == "false":
		return json.RawMessage(v), nil
	case strings.HasPrefix(v, `"`) && strings.HasSuffix(v, `"`) && len(v) >= 2:
		s, err := strconv.Unquote(v)
		if err != nil {
			return nil, errors.New("bad string")
		}
		return json.Marshal(s)
	case strings.HasPrefix(v, "'") && strings.HasSuffix(v, "'") && len(v) >= 2:
		return json.Marshal(v[1 : len(v)-1])
	}
	if n, err := strconv.Atoi(strings.ReplaceAll(v, "_", "")); err == nil {
		return json.Marshal(n)
	}
	return nil, fmt.Errorf("unsupported value %s (use a string, true/false or a number)", v)
}

// stripTOMLComment cuts a # comment that is not inside a string.
func stripTOMLComment(line string) string {
	inStr := byte(0)
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case inStr != 0:
			if c == '\\' && inStr == '"' {
				i++
			} else if c == inStr {
				inStr = 0
			}
		case c == '"' || c == '\'':
			inStr = c
		case c == '#':
			return strings.TrimSpace(line[:i])
		}
	}
	return line
}
