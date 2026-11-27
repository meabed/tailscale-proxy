#!/usr/bin/env node
"use strict";

// Resolves the native `tsp` binary and runs it. Primary path: the per-platform
// optionalDependency package (installed automatically by npm). Fallback: download
// the matching binary from the GitHub release and cache it — so `npx
// tailscale-proxy` always just works, even if the optional package was skipped.

const { spawn, spawnSync } = require("node:child_process");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const REPO = "meabed/tailscale-proxy";
const VERSION = require("../package.json").version;

const PLATFORM = process.platform; // 'darwin' | 'linux' | 'win32'
const ARCH = process.arch; // 'x64' | 'arm64'
const EXE = PLATFORM === "win32" ? "tsp.exe" : "tsp";

// goreleaser archive naming uses Go's GOOS/GOARCH.
const GOOS = PLATFORM === "win32" ? "windows" : PLATFORM;
const GOARCH = ARCH === "x64" ? "amd64" : ARCH;

function fromOptionalDep() {
  try {
    return require.resolve(`tailscale-proxy-${PLATFORM}-${ARCH}/bin/${EXE}`);
  } catch {
    return null;
  }
}

function cacheDir() {
  const base = process.env.XDG_CACHE_HOME || path.join(os.homedir(), ".cache");
  return path.join(base, "tailscale-proxy", `v${VERSION}`);
}

function fromCache() {
  const p = path.join(cacheDir(), EXE);
  return fs.existsSync(p) ? p : null;
}

function downloadToCache() {
  const ext = PLATFORM === "win32" ? "zip" : "tar.gz";
  const asset = `tsp_${GOOS}_${GOARCH}.${ext}`;
  // Prefer the exact version; fall back to the latest release if that asset is
  // missing (e.g. a stale/placeholder launcher version) so it always self-heals.
  const urls = [
    `https://github.com/${REPO}/releases/download/v${VERSION}/${asset}`,
    `https://github.com/${REPO}/releases/latest/download/${asset}`,
  ];
  const dir = cacheDir();
  fs.mkdirSync(dir, { recursive: true });
  const archive = path.join(dir, asset);

  let ok = false;
  for (const url of urls) {
    process.stderr.write(`tailscale-proxy: fetching ${asset}…\n`);
    if (spawnSync("curl", ["-fsSL", "-o", archive, url], { stdio: ["ignore", "ignore", "inherit"] }).status === 0) {
      ok = true;
      break;
    }
  }
  if (!ok) {
    throw new Error(`could not download ${asset} for v${VERSION} or latest`);
  }
  // `tar` extracts both .tar.gz and .zip on macOS/Linux/Windows-10+.
  const ex = spawnSync("tar", ["-xf", archive, "-C", dir], { stdio: ["ignore", "ignore", "inherit"] });
  if (ex.status !== 0) {
    throw new Error(`extract failed: ${archive}`);
  }
  fs.rmSync(archive, { force: true });
  const bin = path.join(dir, EXE);
  if (!fs.existsSync(bin)) {
    throw new Error(`binary ${EXE} not found in ${asset}`);
  }
  fs.chmodSync(bin, 0o755);
  return bin;
}

function resolveBinary() {
  return fromOptionalDep() || fromCache() || downloadToCache();
}

// Runs the binary with this process's arguments and exits with its code. Ctrl-C
// from the terminal reaches the binary directly (one process group), so this
// process only waits for it; a SIGTERM or SIGHUP sent to this process alone is
// passed on, so `tsp run` still releases its name and stops its app.
function main() {
  let bin;
  try {
    bin = resolveBinary();
  } catch (err) {
    console.error(
      `tailscale-proxy: could not obtain a binary for ${PLATFORM}-${ARCH} (v${VERSION}).\n` +
        `${err.message}\n` +
        `Install from source instead: go install github.com/${REPO}@latest\n` +
        `Releases: https://github.com/${REPO}/releases`
    );
    process.exit(1);
  }
  const child = spawn(bin, process.argv.slice(2), { stdio: "inherit" });
  process.on("SIGINT", () => {});
  for (const signal of ["SIGTERM", "SIGHUP"]) {
    process.on(signal, () => child.kill(signal));
  }
  child.on("error", (err) => {
    console.error(err.message);
    process.exit(1);
  });
  child.on("exit", (code, signal) => process.exit(code ?? (signal ? 128 + (os.constants.signals[signal] ?? 0) : 1)));
}

main();
