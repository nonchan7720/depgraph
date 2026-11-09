package version

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nonchan7720/depgraph/pkg/infrastructure/codexhooks"
)

// repoRoot is the repository root (this package is pkg/version).
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// writeFile writes dir/rel, creating its directories.
func writeFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The Claude Code plugin is released with the binary: its version is the
// binary's (release-please bumps both), the marketplace points at it, and
// every hook command and skill it declares exists.
func TestClaudeCodePlugin(t *testing.T) {
	root := repoRoot(t)
	var manifest struct {
		Name, Version, Description, License string
	}
	readJSON(t, filepath.Join(root, "plugin", ".claude-plugin", "plugin.json"), &manifest)
	if manifest.Name != "depgraph" || manifest.Version != Version || manifest.Description == "" || manifest.License != "Apache-2.0" {
		t.Errorf("plugin.json = %+v (version must be %s)", manifest, Version)
	}
	var market struct {
		Name    string
		Owner   struct{ Name string }
		Plugins []struct{ Name, Source string }
	}
	readJSON(t, filepath.Join(root, ".claude-plugin", "marketplace.json"), &market)
	if market.Name == "" || market.Owner.Name == "" || len(market.Plugins) != 1 ||
		market.Plugins[0].Name != manifest.Name || market.Plugins[0].Source != "./plugin" {
		t.Errorf("marketplace.json = %+v", market)
	}

	var hooks struct {
		Hooks map[string][]struct {
			Hooks []struct{ Type, Command string }
		}
	}
	readJSON(t, filepath.Join(root, "plugin", "hooks", "hooks.json"), &hooks)
	script := regexp.MustCompile(`\$\{CLAUDE_PLUGIN_ROOT\}/([^"\s]+)`)
	for _, event := range []string{"SessionStart", "UserPromptSubmit"} {
		if len(hooks.Hooks[event]) == 0 {
			t.Errorf("no %s hook", event)
		}
		for _, g := range hooks.Hooks[event] {
			for _, h := range g.Hooks {
				m := script.FindStringSubmatch(h.Command)
				if h.Type != "command" || m == nil {
					t.Errorf("%s: %+v", event, h)
					continue
				}
				if _, err := os.Stat(filepath.Join(root, "plugin", m[1])); err != nil {
					t.Errorf("%s runs a missing script: %v", event, err)
				}
			}
		}
	}

	skills, err := filepath.Glob(filepath.Join(root, "plugin", "skills", "*", "SKILL.md"))
	if err != nil || len(skills) < 2 {
		t.Fatalf("skills: %v %v", skills, err)
	}
	front := regexp.MustCompile(`(?s)\A---\nname: ([a-z0-9-]+)\ndescription: (.+?)\n.*?---\n`)
	for _, s := range skills {
		b, _ := os.ReadFile(s)
		m := front.FindSubmatch(b)
		if m == nil || string(m[1]) != filepath.Base(filepath.Dir(s)) {
			t.Errorf("%s: frontmatter needs name (the directory) and description", s)
		}
	}
}

// The hook script runs depgraph's hook only where nothing else does: never
// without a depgraph binary (here: on PATH, the release cannot be reached), not when the project's settings.json runs the
// hooks itself, and not session-start when claude-hook-gate's session_start
// runs it (the context would be added twice). stdin reaches depgraph.
func TestPluginHookScript(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	script := filepath.Join(repoRoot(t), "plugin", "scripts", "hook.sh")
	bin, cache := t.TempDir(), t.TempDir()
	fake := "#!/bin/sh\necho \"depgraph $*\"\ncat\n"
	if err := os.WriteFile(filepath.Join(bin, "depgraph"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(project, path, event string) string {
		t.Helper()
		cmd := exec.Command("sh", script, event)
		cmd.Env = []string{"PATH=" + path, "CLAUDE_PROJECT_DIR=" + project,
			"DEPGRAPH_CACHE_DIR=" + cache, "DEPGRAPH_RELEASE_BASE_URL=file:///nonexistent"}
		cmd.Stdin = strings.NewReader(`{"session_id":"s1"}`)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", event, err, out)
		}
		return string(out)
	}
	sys := "/usr/bin:/bin"
	path := bin + ":" + sys

	plain := t.TempDir()
	if got := run(plain, path, "session-start"); got != "depgraph hook session-start\n{\"session_id\":\"s1\"}" {
		t.Errorf("session-start = %q", got)
	}
	if got := run(plain, path, "prompt"); !strings.HasPrefix(got, "depgraph hook prompt\n") {
		t.Errorf("prompt = %q", got)
	}
	if got := run(plain, sys, "prompt"); got != "" {
		t.Errorf("without depgraph = %q", got)
	}
	if got := run(plain, path, "nonsense"); got != "" {
		t.Errorf("unknown event = %q", got)
	}

	gate := t.TempDir()
	writeFile(t, gate, ".claude/gate.yaml", "session_start:\n  - { name: depgraph, cmd: \"depgraph hook session-start --format text\" }\n")
	if got := run(gate, path, "session-start"); got != "" {
		t.Errorf("session-start with gate.yaml's = %q", got)
	}
	if got := run(gate, path, "prompt"); !strings.HasPrefix(got, "depgraph hook prompt") {
		t.Errorf("prompt with gate.yaml = %q", got)
	}

	own := t.TempDir()
	writeFile(t, own, ".claude/settings.json", `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"depgraph hook prompt"}]}]}}`)
	for _, ev := range []string{"session-start", "prompt"} {
		if got := run(own, path, ev); got != "" {
			t.Errorf("%s with the project's own hooks = %q", ev, got)
		}
	}
}

// Under Codex (hook.sh <event> codex) the hook stays quiet where something
// else adds the same context: the project or the user runs `depgraph hook`
// in a Codex config layer, claude-hook-gate's session_start runs it, or — in
// the plugin layer (Codex passes PLUGIN_ROOT) — `depgraph codex install-hooks`
// put a copy of the hooks into a config layer and that copy's script exists
// (Codex runs both; the copy is the one that works where plugin hooks do not).
func TestPluginHookScriptCodex(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	plugin := filepath.Join(repoRoot(t), "plugin")
	script := filepath.Join(plugin, "scripts", "hook.sh")
	bin, cache := t.TempDir(), t.TempDir()
	fake := "#!/bin/sh\necho \"depgraph $*\"\n"
	if err := os.WriteFile(filepath.Join(bin, "depgraph"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	type env struct{ project, codexHome, system, pluginRoot, claudeRoot string }
	run := func(e env, args ...string) string {
		t.Helper()
		cmd := exec.Command("sh", append([]string{script}, args...)...)
		cmd.Dir = e.project // Codex runs hooks in the session cwd and sets no project dir
		cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + t.TempDir(),
			"CODEX_HOME=" + e.codexHome, "DEPGRAPH_CODEX_SYSTEM_DIR=" + e.system,
			"DEPGRAPH_CACHE_DIR=" + cache, "DEPGRAPH_RELEASE_BASE_URL=file:///nonexistent"}
		if e.pluginRoot != "" {
			cmd.Env = append(cmd.Env, "PLUGIN_ROOT="+e.pluginRoot, "CLAUDE_PLUGIN_ROOT="+e.pluginRoot)
		}
		if e.claudeRoot != "" {
			cmd.Env = append(cmd.Env, "CLAUDE_PLUGIN_ROOT="+e.claudeRoot, "CLAUDE_PROJECT_DIR="+e.project)
		}
		cmd.Stdin = strings.NewReader(`{"session_id":"s1"}`)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	copyOf := func(root string) string {
		t.Helper()
		var out bytes.Buffer
		if err := codexhooks.Install(codexhooks.InstallOptions{Root: root, Print: true}, &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	fresh := func() env {
		return env{project: t.TempDir(), codexHome: t.TempDir(), system: t.TempDir(), pluginRoot: plugin}
	}

	e := fresh()
	for _, ev := range []string{"session-start", "prompt"} {
		if got := run(e, ev, "codex"); got != "depgraph hook "+ev+"\n" {
			t.Errorf("plugin layer %s = %q", ev, got)
		}
	}
	if got := run(e, "prompt", "nonsense"); got != "" {
		t.Errorf("unknown host = %q", got)
	}

	// the project (or the user) runs depgraph's hooks itself
	for _, at := range []struct {
		base func(env) string
		rel  string
	}{
		{func(e env) string { return e.project }, ".codex/hooks.json"},
		{func(e env) string { return e.project }, ".codex/config.toml"},
		{func(e env) string { return e.codexHome }, "hooks.json"},
		{func(e env) string { return e.system }, "hooks.json"},
	} {
		e := fresh()
		writeFile(t, at.base(e), at.rel, `"command": "depgraph hook prompt"`)
		for _, layer := range []string{plugin, ""} {
			e.pluginRoot = layer
			if got := run(e, "prompt", "codex"); got != "" {
				t.Errorf("%s runs depgraph hook, layer %q: %q", at.rel, layer, got)
			}
		}
	}

	// claude-hook-gate's session_start (Codex reads .agents/ first)
	e = fresh()
	writeFile(t, e.project, ".agents/gate.yaml", "session_start:\n  - { name: depgraph, cmd: \"depgraph hook session-start --format text\" }\n")
	if got := run(e, "session-start", "codex"); got != "" {
		t.Errorf("session-start with .agents/gate.yaml = %q", got)
	}
	if got := run(e, "prompt", "codex"); got != "depgraph hook prompt\n" {
		t.Errorf("prompt with .agents/gate.yaml = %q", got)
	}

	// a copy in a config layer: the plugin layer leaves the work to it
	for _, at := range []func(env) string{
		func(e env) string { return filepath.Join(e.codexHome, "hooks.json") },
		func(e env) string { return filepath.Join(e.project, ".codex", "hooks.json") },
		func(e env) string { return filepath.Join(e.system, "hooks.json") },
	} {
		e := fresh()
		target := at(e)
		writeFile(t, filepath.Dir(target), filepath.Base(target), copyOf(plugin))
		if got := run(e, "session-start", "codex"); got != "" {
			t.Errorf("plugin layer beside a copy in %s = %q", target, got)
		}
		e.pluginRoot = "" // the copy itself
		if got := run(e, "session-start", "codex"); got != "depgraph hook session-start\n" {
			t.Errorf("the copy in %s = %q", target, got)
		}
	}
	// a copy whose plugin directory is gone (an old version) does not count
	e = fresh()
	gone := filepath.Join(t.TempDir(), "depgraph", "0.0.1")
	if err := os.MkdirAll(filepath.Join(gone, "codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	tmpl, _ := os.ReadFile(filepath.Join(plugin, "codex", "hooks.json"))
	writeFile(t, gone, "codex/hooks.json", string(tmpl))
	writeFile(t, e.codexHome, "hooks.json", copyOf(gone))
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	if got := run(e, "prompt", "codex"); got != "depgraph hook prompt\n" {
		t.Errorf("plugin layer beside a stale copy = %q", got)
	}

	// Claude Code never looks at Codex's files
	e = fresh()
	writeFile(t, e.codexHome, "hooks.json", copyOf(plugin)+`"depgraph hook prompt"`)
	e.pluginRoot, e.claudeRoot = "", plugin
	if got := run(e, "prompt"); got != "depgraph hook prompt\n" {
		t.Errorf("Claude Code beside Codex's copy = %q", got)
	}
}

// The Codex plugin is the same plugin/ directory: both Codex manifests carry
// the binary's version (release-please bumps them), point at the Codex hooks
// (whose scripts exist and run hook.sh with the codex host), and the Codex
// marketplace (.agents/plugins/marketplace.json) lists it.
func TestCodexPlugin(t *testing.T) {
	root := repoRoot(t)
	plugin := filepath.Join(root, "plugin")
	var claude struct{ Description string }
	readJSON(t, filepath.Join(plugin, ".claude-plugin", "plugin.json"), &claude)

	type ui struct{ DisplayName, ShortDescription, Category string }
	var legacy struct {
		Name, Version, Description, License, Skills, Hooks string
		Interface                                          ui
	}
	readJSON(t, filepath.Join(plugin, ".codex-plugin", "plugin.json"), &legacy)
	if legacy.Name != "depgraph" || legacy.Version != Version || legacy.License != "Apache-2.0" || legacy.Description == "" ||
		legacy.Skills != "./skills/" || legacy.Hooks != "./codex/hooks.json" || legacy.Interface.DisplayName == "" {
		t.Errorf(".codex-plugin/plugin.json = %+v (version must be %s)", legacy, Version)
	}
	var portable struct {
		Name, Version, Description, License string
		Extensions                          map[string]struct {
			Hooks     string
			Interface ui
		}
	}
	readJSON(t, filepath.Join(plugin, "plugin.json"), &portable)
	oa := portable.Extensions["com.openai"]
	if portable.Name != legacy.Name || portable.Version != Version || portable.Description != legacy.Description ||
		portable.License != legacy.License || oa.Hooks != legacy.Hooks || oa.Interface != legacy.Interface {
		t.Errorf("plugin.json = %+v, want the same as .codex-plugin/plugin.json", portable)
	}
	if _, err := os.Stat(filepath.Join(plugin, "skills")); err != nil {
		t.Error(err)
	}

	var market struct {
		Name    string
		Plugins []struct {
			Name     string
			Source   struct{ Source, Path string }
			Policy   struct{ Installation, Authentication string }
			Category string
		}
	}
	readJSON(t, filepath.Join(root, ".agents", "plugins", "marketplace.json"), &market)
	if market.Name != "depgraph" || len(market.Plugins) != 1 {
		t.Fatalf("marketplace.json = %+v", market)
	}
	if p := market.Plugins[0]; p.Name != legacy.Name || p.Source.Source != "local" || p.Source.Path != "./plugin" ||
		p.Policy.Installation == "" || p.Policy.Authentication == "" || p.Category == "" {
		t.Errorf("marketplace.json plugin = %+v", p)
	}

	// no top-level "description": some Codex builds reject the whole file for it
	var hooks map[string]json.RawMessage
	readJSON(t, filepath.Join(plugin, "codex", "hooks.json"), &hooks)
	if len(hooks) != 1 || hooks["hooks"] == nil {
		t.Errorf("codex/hooks.json keys: %v", hooks)
	}
	var tmpl struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Type, Command string
				Timeout       int
			}
		}
	}
	readJSON(t, filepath.Join(plugin, "codex", "hooks.json"), &tmpl)
	cmd := regexp.MustCompile(`^sh "\$\{PLUGIN_ROOT\}/(scripts/hook\.sh)" (session-start|prompt) codex$`)
	for event, arg := range map[string]string{"SessionStart": "session-start", "UserPromptSubmit": "prompt"} {
		if len(tmpl.Hooks[event]) != 1 || len(tmpl.Hooks[event][0].Hooks) != 1 {
			t.Errorf("%s: %+v", event, tmpl.Hooks[event])
			continue
		}
		h := tmpl.Hooks[event][0].Hooks[0]
		m := cmd.FindStringSubmatch(h.Command)
		if h.Type != "command" || m == nil || m[2] != arg || h.Timeout == 0 || !codexhooks.IsPluginCommand(h.Command) {
			t.Errorf("%s: %+v", event, h)
			continue
		}
		if _, err := os.Stat(filepath.Join(plugin, m[1])); err != nil {
			t.Error(err)
		}
	}
	if len(tmpl.Hooks) != 2 {
		t.Errorf("codex/hooks.json events: %v", tmpl.Hooks)
	}
}

// install-hooks.sh runs `depgraph codex install-hooks` of the plugin's binary
// with the plugin root (its own parent), passing the arguments through.
func TestCodexInstallHooksScript(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	plugin := filepath.Join(repoRoot(t), "plugin")
	bin := filepath.Join(t.TempDir(), "depgraph")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"$*\"\nexit 4\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", filepath.Join(plugin, "codex", "install-hooks.sh"), "--repo", "--root", "a b")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "DEPGRAPH_BIN=" + bin}
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 4 {
		t.Fatalf("exit: %v\n%s", err, out)
	}
	if want := "codex install-hooks --plugin-root " + plugin + " --repo --root a b\n"; string(out) != want {
		t.Errorf("install-hooks.sh ran %q, want %q", out, want)
	}
}

// setup.sh updates the plugin through codex, then runs install-hooks.sh of
// the newest version in Codex's plugin cache with the remaining arguments.
func TestCodexSetupScript(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	setup := filepath.Join(repoRoot(t), "plugin", "codex", "setup.sh")
	home := t.TempDir()
	cache := filepath.Join(home, "plugins", "cache", "depgraph", "depgraph")
	for _, v := range []string{"0.1.9", "0.10.0", "0.10.0-rc.1", "0.2.0", "not-a-version"} {
		dir := filepath.Join(cache, v, "codex")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "install-hooks.sh"), []byte("echo \""+v+" $*\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	codex := "#!/bin/sh\necho \"$*\" >>" + calls + "\n[ \"$3\" = upgrade ] && exit 1\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(codex), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(env []string, args ...string) (string, string, int) {
		t.Helper()
		cmd := exec.Command("bash", append([]string{setup}, args...)...)
		cmd.Env = append([]string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + t.TempDir(), "CODEX_HOME=" + home}, env...)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		code := 0
		if err := cmd.Run(); err != nil {
			ee, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatal(err)
			}
			code = ee.ExitCode()
		}
		return out.String(), errb.String(), code
	}

	out, stderr, code := run(nil, "--repo", "--no-update")
	if code != 0 || out != "0.10.0 --repo\n" {
		t.Errorf("--no-update = %q %q %d", out, stderr, code)
	}
	if _, err := os.Stat(calls); err == nil {
		t.Error("--no-update ran codex")
	}
	out, stderr, code = run(nil, "--print")
	if code != 0 || out != "0.10.0 --print\n" {
		t.Errorf("setup = %q %q %d", out, stderr, code)
	}
	b, _ := os.ReadFile(calls)
	if want := "plugin marketplace upgrade depgraph\nplugin marketplace add nonchan7720/depgraph\nplugin add depgraph@depgraph\n"; string(b) != want {
		t.Errorf("codex calls = %q, want %q", b, want)
	}

	// DEPGRAPH_PLUGIN_DIR: that plugin, no update
	if err := os.Remove(calls); err != nil {
		t.Fatal(err)
	}
	out, _, code = run([]string{"DEPGRAPH_PLUGIN_DIR=" + filepath.Join(cache, "0.1.9") + "/"})
	if code != 0 || out != "0.1.9 \n" {
		t.Errorf("DEPGRAPH_PLUGIN_DIR = %q %d", out, code)
	}
	if _, err := os.Stat(calls); err == nil {
		t.Error("DEPGRAPH_PLUGIN_DIR ran codex")
	}

	// nothing installed
	_, stderr, code = run([]string{"CODEX_HOME=" + t.TempDir()}, "--no-update")
	if code != 1 || !strings.Contains(stderr, "codex plugin marketplace add nonchan7720/depgraph") {
		t.Errorf("nothing installed = %q %d", stderr, code)
	}
}
