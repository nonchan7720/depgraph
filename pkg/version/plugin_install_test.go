package version

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The plugin installs the depgraph binary of its own version: the hooks and
// the scripts/depgraph wrapper (what the skills run) take DEPGRAPH_BIN, else
// the cached binary, else download it from the GitHub release v<version>
// (GoReleaser's depgraph_<os>_<arch>.tar.gz, verified against checksums.txt),
// else depgraph on PATH. They never install anything they could not verify.

// fakeDepgraph is the binary inside the fake release: it prints its name,
// its arguments and stdin.
const fakeDepgraph = "#!/bin/sh\necho \"released $*\"\ncat\n"

// releasePlatform is GoReleaser's {{ .Os }}_{{ .Arch }} for this machine, or
// "" where the install script does not download.
func releasePlatform() string {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return ""
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return ""
	}
	return runtime.GOOS + "_" + runtime.GOARCH
}

type installEnv struct {
	t      *testing.T
	dir    string
	bin    string // the only directory on PATH: the commands a test links in
	root   string // plugin root with .claude-plugin/plugin.json
	cache  string
	base   string // release base (file://)
	plugin string // the repository's plugin/ directory
}

func newInstallEnv(t *testing.T, version string) *installEnv {
	t.Helper()
	if releasePlatform() == "" {
		t.Skip("no release for " + runtime.GOOS + "/" + runtime.GOARCH)
	}
	for _, c := range []string{"sh", "curl", "tar", "gzip"} {
		if _, err := exec.LookPath(c); err != nil {
			t.Skip("no " + c)
		}
	}
	dir := t.TempDir()
	e := &installEnv{
		t:      t,
		dir:    dir,
		bin:    filepath.Join(dir, "bin"),
		root:   filepath.Join(dir, "plugin-root"),
		cache:  filepath.Join(dir, "cache"),
		base:   filepath.Join(dir, "release"),
		plugin: filepath.Join(repoRoot(t), "plugin"),
	}
	if err := os.MkdirAll(e.bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// the plugin root under test: the repository's scripts with a chosen version
	if err := os.MkdirAll(filepath.Join(e.root, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.write(filepath.Join(e.root, ".claude-plugin", "plugin.json"), fmt.Sprintf("{\n  \"name\": \"depgraph\",\n  \"version\": %q\n}\n", version), 0o644)
	if err := os.Symlink(filepath.Join(e.plugin, "scripts"), filepath.Join(e.root, "scripts")); err != nil {
		t.Fatal(err)
	}
	e.link("sh", "cat", "sed", "head", "tr", "dirname", "grep", "uname", "mkdir", "mktemp", "rm", "mv", "chmod", "find", "touch", "tar", "gzip", "curl")
	if !e.link("sha256sum") && !e.link("shasum") {
		t.Skip("no sha256sum or shasum")
	}
	e.link("perl") // shasum is a perl script
	return e
}

func (e *installEnv) write(path, body string, mode os.FileMode) {
	e.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		e.t.Fatal(err)
	}
}

// link puts commands on the test's PATH; false when one is not installed.
func (e *installEnv) link(names ...string) bool {
	e.t.Helper()
	ok := true
	for _, n := range names {
		real, err := exec.LookPath(n)
		if err != nil {
			ok = false
			continue
		}
		_ = os.Symlink(real, filepath.Join(e.bin, n))
	}
	return ok
}

func (e *installEnv) unlink(names ...string) {
	for _, n := range names {
		_ = os.Remove(filepath.Join(e.bin, n))
	}
}

// release writes <base>/v<version>/depgraph_<platform>.tar.gz and
// checksums.txt; tamper makes the listed hash wrong.
func (e *installEnv) release(version string, tamper bool) {
	e.t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name, body string
		mode       int64
	}{{"depgraph", fakeDepgraph, 0o755}, {"LICENSE", "Apache-2.0\n", 0o644}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.body)), Typeflag: tar.TypeReg}); err != nil {
			e.t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			e.t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		e.t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		e.t.Fatal(err)
	}
	archive := "depgraph_" + releasePlatform() + ".tar.gz"
	dir := filepath.Join(e.base, "v"+version)
	e.write(filepath.Join(dir, archive), buf.String(), 0o644)
	sum := sha256.Sum256(buf.Bytes())
	hash := hex.EncodeToString(sum[:])
	if tamper {
		hash = strings.Repeat("0", 64)
	}
	e.write(filepath.Join(dir, "checksums.txt"), strings.Repeat("a", 64)+"  depgraph_windows_amd64.zip\n"+hash+"  "+archive+"\n", 0o644)
}

type result struct {
	stdout, stderr string
	code           int
}

// run runs a plugin script (relative to the plugin root) with a clean environment.
func (e *installEnv) run(script string, env map[string]string, args ...string) result {
	e.t.Helper()
	cmd := exec.Command(filepath.Join(e.bin, "sh"), append([]string{filepath.Join(e.root, script)}, args...)...)
	vars := map[string]string{
		"PATH":                      e.bin,
		"HOME":                      e.dir,
		"CLAUDE_PROJECT_DIR":        e.dir,
		"CLAUDE_PLUGIN_ROOT":        e.root,
		"DEPGRAPH_CACHE_DIR":        e.cache,
		"DEPGRAPH_RELEASE_BASE_URL": "file://" + e.base,
	}
	maps.Copy(vars, env)
	for k, v := range vars {
		if v != "" {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	cmd.Stdin = strings.NewReader(`{"session_id":"s1"}`)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			e.t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	return result{out.String(), errb.String(), code}
}

func (e *installEnv) cached(version string) string {
	return filepath.Join(e.cache, version, "depgraph")
}

func notice(version, reason string) string {
	return "depgraph: could not install the depgraph v" + version + " binary (" + reason + ")\n"
}

func TestPluginInstallsBinaryFromRelease(t *testing.T) {
	e := newInstallEnv(t, "1.2.3")
	e.release("1.2.3", false)

	r := e.run("scripts/hook.sh", nil, "session-start")
	if r.code != 0 || r.stdout != "released hook session-start\n{\"session_id\":\"s1\"}" || r.stderr != "" {
		t.Fatalf("first session-start = %+v", r)
	}
	if fi, err := os.Stat(e.cached("1.2.3")); err != nil || fi.Mode()&0o111 == 0 {
		t.Fatalf("cached binary: %v %v", fi, err)
	}
	// no leftovers of the download next to the binary
	if left, _ := filepath.Glob(filepath.Join(e.cache, "1.2.3", ".download.*")); len(left) != 0 {
		t.Errorf("leftovers: %v", left)
	}

	// later runs use the cache: no download tool needed, the release may be gone
	e.unlink("curl")
	if err := os.RemoveAll(e.base); err != nil {
		t.Fatal(err)
	}
	if r := e.run("scripts/hook.sh", nil, "prompt"); r.code != 0 || !strings.HasPrefix(r.stdout, "released hook prompt\n") {
		t.Errorf("prompt from the cache = %+v", r)
	}
	// the wrapper the skills run passes the arguments and the exit code through
	e.write(e.cached("1.2.3"), "#!/bin/sh\necho \"released $*\"\nexit 3\n", 0o755)
	if r := e.run("scripts/depgraph", nil, "check", "--root", "a b"); r.code != 3 || r.stdout != "released check --root a b\n" {
		t.Errorf("wrapper = %+v", r)
	}
}

func TestPluginWrapperInstalls(t *testing.T) {
	e := newInstallEnv(t, "1.2.3")
	e.release("1.2.3", false)
	if r := e.run("scripts/depgraph", nil, "version"); r.code != 0 || !strings.HasPrefix(r.stdout, "released version\n") {
		t.Fatalf("wrapper = %+v", r)
	}
	if _, err := os.Stat(e.cached("1.2.3")); err != nil {
		t.Fatal(err)
	}
	// without CLAUDE_PLUGIN_ROOT (the Bash tool does not export it) the root is the script's parent
	if r := e.run("scripts/depgraph", map[string]string{"CLAUDE_PLUGIN_ROOT": ""}, "version"); r.code != 0 || !strings.HasPrefix(r.stdout, "released version\n") {
		t.Errorf("wrapper without CLAUDE_PLUGIN_ROOT = %+v", r)
	}
}

func TestPluginInstallFailsClosed(t *testing.T) {
	onPath := "#!/bin/sh\necho \"on-path $*\"\n"
	for _, tc := range []struct {
		name   string
		setup  func(e *installEnv)
		reason string
	}{
		{"checksum mismatch", func(e *installEnv) { e.release("1.2.3", true) }, "checksum-mismatch"},
		{"no release", func(e *installEnv) {}, "download-failed"},
		{"no sha256 tool", func(e *installEnv) { e.release("1.2.3", false); e.unlink("sha256sum", "shasum") }, "no-sha256-tool"},
		{"no download tool", func(e *installEnv) { e.release("1.2.3", false); e.unlink("curl") }, "no-download-tool"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newInstallEnv(t, "1.2.3")
			tc.setup(e)
			r := e.run("scripts/hook.sh", nil, "session-start")
			if r.code != 0 || r.stdout != "" || r.stderr != notice("1.2.3", tc.reason) {
				t.Errorf("hook = %+v", r)
			}
			if _, err := os.Stat(e.cached("1.2.3")); err == nil {
				t.Error("installed anyway")
			}
			if r := e.run("scripts/depgraph", nil, "version"); r.code != 127 || r.stderr != notice("1.2.3", tc.reason)+"depgraph: not found (install it from https://github.com/nonchan7720/depgraph/releases)\n" {
				t.Errorf("wrapper = %+v", r)
			}
			// depgraph on PATH is the fallback, without a notice
			e.write(filepath.Join(e.bin, "depgraph"), onPath, 0o755)
			if r := e.run("scripts/hook.sh", nil, "session-start"); r.code != 0 || r.stdout != "on-path hook session-start\n" || r.stderr != "" {
				t.Errorf("hook with depgraph on PATH = %+v", r)
			}
			if r := e.run("scripts/depgraph", nil, "version"); r.code != 0 || r.stdout != "on-path version\n" {
				t.Errorf("wrapper with depgraph on PATH = %+v", r)
			}
		})
	}
}

// The prompt hook runs on every prompt: it never downloads (session-start
// does), so a missing binary costs nothing.
func TestPluginPromptHookDoesNotDownload(t *testing.T) {
	e := newInstallEnv(t, "1.2.3")
	e.release("1.2.3", false)
	if r := e.run("scripts/hook.sh", nil, "prompt"); r.code != 0 || r.stdout != "" || r.stderr != "" {
		t.Errorf("prompt = %+v", r)
	}
	if _, err := os.Stat(e.cached("1.2.3")); err == nil {
		t.Error("prompt downloaded the binary")
	}
}

func TestPluginInstallBadVersion(t *testing.T) {
	e := newInstallEnv(t, "1.2.3/../x")
	r := e.run("scripts/hook.sh", nil, "session-start")
	if r.code != 0 || r.stdout != "" || r.stderr != notice("1.2.3..x", "bad-version") {
		t.Errorf("hook = %+v", r)
	}
}

func TestPluginInstallDEPGRAPHBIN(t *testing.T) {
	e := newInstallEnv(t, "1.2.3")
	own := filepath.Join(e.dir, "own-depgraph")
	e.write(own, "#!/bin/sh\necho \"own $*\"\n", 0o755)
	if r := e.run("scripts/hook.sh", map[string]string{"DEPGRAPH_BIN": own}, "prompt"); r.code != 0 || r.stdout != "own hook prompt\n" {
		t.Errorf("hook = %+v", r)
	}
	if r := e.run("scripts/depgraph", map[string]string{"DEPGRAPH_BIN": own}, "version"); r.stdout != "own version\n" {
		t.Errorf("wrapper = %+v", r)
	}
}

// A new install removes cached versions nobody used for 30 days; the ones in
// use (touched by every run) stay.
func TestPluginInstallPrunesOldVersions(t *testing.T) {
	e := newInstallEnv(t, "1.2.3")
	e.release("1.2.3", false)
	old := filepath.Join(e.cache, "1.0.0")
	recent := filepath.Join(e.cache, "1.1.0")
	other := filepath.Join(e.cache, "keep-me")
	for _, d := range []string{old, recent, other} {
		e.write(filepath.Join(d, "depgraph"), fakeDepgraph, 0o755)
	}
	past := time.Now().Add(-40 * 24 * time.Hour)
	for _, d := range []string{old, other} {
		if err := os.Chtimes(d, past, past); err != nil {
			t.Fatal(err)
		}
	}
	if r := e.run("scripts/hook.sh", nil, "session-start"); r.code != 0 || r.stderr != "" {
		t.Fatalf("hook = %+v", r)
	}
	for d, want := range map[string]bool{old: false, recent: true, other: true, filepath.Join(e.cache, "1.2.3"): true} {
		if _, err := os.Stat(d); (err == nil) != want {
			t.Errorf("%s exists = %v, want %v", d, err == nil, want)
		}
	}

	// a cached version in use is touched, so it is not pruned later
	if err := os.Chtimes(filepath.Join(e.cache, "1.2.3"), past, past); err != nil {
		t.Fatal(err)
	}
	e.run("scripts/hook.sh", nil, "prompt")
	if fi, err := os.Stat(filepath.Join(e.cache, "1.2.3")); err != nil || time.Since(fi.ModTime()) > time.Hour {
		t.Errorf("in-use version not touched: %v %v", fi, err)
	}
}
