package core

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// The local HTTPS proxy keeps itself current: once a day it asks GitHub for the
// latest release (a conditional request) and, after checking the archive against
// the release's checksums.txt, installs a newer one in bin/ (never one that failed
// here before); the supervisor then swaps to it once it proves it serves, and keeps
// the old one serving if it does not. The CLI only says, once a day, that a release
// is out and how to get it (brew, npm or tsp update). Off: `tsp configure
// --auto-update=false`, TSP_NO_UPDATE=1, or CI.

// updateState is ~/.tsp/update.json.
type updateState struct {
	CheckedAt  time.Time `json:"checkedAt"`
	ETag       string    `json:"etag,omitempty"`
	Latest     string    `json:"latest,omitempty"`
	NotifiedAt time.Time `json:"notifiedAt"`
}

func updateStatePath(dir string) string { return filepath.Join(dir, "update.json") }

func loadUpdateState(dir string) updateState {
	var st updateState
	if data, err := os.ReadFile(updateStatePath(dir)); err == nil {
		_ = json.Unmarshal(data, &st)
	}
	return st
}

func saveUpdateState(dir string, st updateState) {
	data, _ := json.MarshalIndent(st, "", "  ")
	_ = writeFileAtomic(updateStatePath(dir), data, 0o644)
}

// Where releases come from, and how often to look; replaced in tests.
var (
	releaseAPI       = "https://api.github.com/repos/" + repoSlug + "/releases/latest"
	releaseDownload  = "https://github.com/" + repoSlug + "/releases/download"
	updateHTTP       = &http.Client{Timeout: 60 * time.Second}
	updateEvery      = 24 * time.Hour
	updateFirstDelay = func() time.Duration { return time.Minute + rand.N(9*time.Minute) } // jitter: machines don't all ask at boot
)

// autoUpdateEnabled is the config switch, overridden off by TSP_NO_UPDATE and CI.
func autoUpdateEnabled() bool {
	if os.Getenv("TSP_NO_UPDATE") != "" || os.Getenv("CI") != "" {
		return false
	}
	cfg, _, _, _ := LoadConfig()
	return cfg.AutoUpdate
}

// checkLatest refreshes st.Latest, sending the saved ETag so an unchanged answer
// costs GitHub nothing (304).
func checkLatest(ctx context.Context, st *updateState) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseAPI, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "tailscale-proxy/"+Version)
	req.Header.Set("Accept", "application/vnd.github+json")
	if st.ETag != "" && st.Latest != "" {
		req.Header.Set("If-None-Match", st.ETag)
	}
	resp, err := updateHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	st.CheckedAt = time.Now()
	switch resp.StatusCode {
	case http.StatusNotModified:
		return nil
	case http.StatusOK:
	default:
		return fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil || rel.TagName == "" {
		return fmt.Errorf("no release in GitHub's answer: %v", err)
	}
	st.Latest, st.ETag = rel.TagName, resp.Header.Get("ETag")
	return nil
}

// releaseArchiveName is the archive goreleaser publishes for goos/goarch.
func releaseArchiveName(goos, goarch string) string {
	if goos == "windows" {
		return fmt.Sprintf("tsp_%s_%s.zip", goos, goarch)
	}
	return fmt.Sprintf("tsp_%s_%s.tar.gz", goos, goarch)
}

// fetchReleaseBinary downloads tag's archive for this platform, checks its sha256
// against the release's checksums.txt, and returns the tsp binary inside it.
func fetchReleaseBinary(ctx context.Context, tag string) ([]byte, error) {
	name := releaseArchiveName(runtime.GOOS, runtime.GOARCH)
	sums, err := fetch(ctx, releaseDownload+"/"+tag+"/checksums.txt")
	if err != nil {
		return nil, fmt.Errorf("checksums.txt: %v", err)
	}
	want := checksumFor(sums, name)
	if want == "" {
		return nil, fmt.Errorf("checksums.txt of %s lists no %s", tag, name)
	}
	archive, err := fetch(ctx, releaseDownload+"/"+tag+"/"+name)
	if err != nil {
		return nil, err
	}
	if got := sha256.Sum256(archive); hex.EncodeToString(got[:]) != want {
		return nil, fmt.Errorf("%s does not match its checksum: not installed", name)
	}
	binName := "tsp"
	if runtime.GOOS == "windows" {
		binName = "tsp.exe"
	}
	var out bytes.Buffer
	if strings.HasSuffix(name, ".zip") {
		err = extractZipEntry(bytes.NewReader(archive), binName, &out)
	} else {
		err = extractTarGzEntry(bytes.NewReader(archive), binName, &out)
	}
	return out.Bytes(), err
}

// checksumFor finds name's sha256 in a checksums.txt ("<hex>  <name>" per line).
func checksumFor(sums []byte, name string) string {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 2 && f[1] == name {
			return strings.ToLower(f[0])
		}
	}
	return ""
}

func fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "tailscale-proxy/"+Version)
	resp, err := updateHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 256<<20))
}

// updateProxyBin checks for a release and, when it is newer than both this proxy
// and the copy in bin/, installs it there. It reports the release it installed.
func updateProxyBin(ctx context.Context, dir string) (string, error) {
	st := loadUpdateState(dir)
	err := checkLatest(ctx, &st)
	if st.CheckedAt.IsZero() {
		st.CheckedAt = time.Now() // offline: try again tomorrow, not at every start
	}
	saveUpdateState(dir, st)
	if err != nil {
		return "", err
	}
	have := Version
	if v := proxyBinVersion(dir); v != "" && versionLess(have, v) {
		have = v
	}
	if st.Latest == "" || !versionLess(have, st.Latest) || isBadVersion(dir, st.Latest) {
		return "", nil
	}
	bin, err := fetchReleaseBinary(ctx, st.Latest)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(binDir(dir), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(binDir(dir), ".tsp-update-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	tag := normalizeVer(st.Latest)
	return tag, installProxyBin(dir, tmp.Name(), tag)
}

// autoUpdateLoop runs updateProxyBin about once a day while the proxy serves,
// first after a jittered delay (or when the last check is a day old).
func autoUpdateLoop(ctx context.Context, dir string) {
	wait := updateFirstDelay()
	if last := loadUpdateState(dir).CheckedAt; !last.IsZero() {
		wait = max(wait, time.Until(last.Add(updateEvery)))
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = updateEvery
		if !autoUpdateEnabled() {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		tag, err := updateProxyBin(cctx, dir)
		cancel()
		switch {
		case err != nil:
			appLog().Warn("auto-update failed", "err", err, "retry", updateEvery.String())
		case tag != "":
			appLog().Info("auto-update installed", "release", tag, "path", proxyBinPath(dir, tag))
		default:
			appLog().Info("auto-update checked", "latest", normalizeVer(loadUpdateState(dir).Latest))
		}
	}
}

// upgradeCommand is how the user updates the tsp at exe themselves.
func upgradeCommand(exe string) string {
	switch installMethod(exe) {
	case "brew":
		return "brew upgrade tsp"
	case "npm":
		return "npm install -g tailscale-proxy@latest"
	case "npx":
		return "npx tailscale-proxy@latest"
	}
	return "tsp update"
}

// updateNotice returns the once-a-day line about a newer release for the CLI at
// exe ("" when there is nothing to say), and records that it said it.
func updateNotice(dir, exe string, now time.Time) string {
	st := loadUpdateState(dir)
	if st.Latest == "" || !versionLess(Version, st.Latest) || now.Sub(st.NotifiedAt) < updateEvery {
		return ""
	}
	st.NotifiedAt = now
	saveUpdateState(dir, st)
	line := fmt.Sprintf("tsp %s is out (this is %s): %s", normalizeVer(st.Latest), Version, upgradeCommand(exe))
	if proxyBinVersion(dir) != "" {
		line += "; the local HTTPS proxy updates itself"
	}
	return line
}
