package version

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
	write := func(dir, rel, body string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

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
	write(gate, ".claude/gate.yaml", "session_start:\n  - { name: depgraph, cmd: \"depgraph hook session-start --format text\" }\n")
	if got := run(gate, path, "session-start"); got != "" {
		t.Errorf("session-start with gate.yaml's = %q", got)
	}
	if got := run(gate, path, "prompt"); !strings.HasPrefix(got, "depgraph hook prompt") {
		t.Errorf("prompt with gate.yaml = %q", got)
	}

	own := t.TempDir()
	write(own, ".claude/settings.json", `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"depgraph hook prompt"}]}]}}`)
	for _, ev := range []string{"session-start", "prompt"} {
		if got := run(own, path, ev); got != "" {
			t.Errorf("%s with the project's own hooks = %q", ev, got)
		}
	}
}
