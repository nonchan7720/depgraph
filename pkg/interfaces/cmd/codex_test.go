package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The repository's plugin directory is a plugin root for install-hooks.
func repoPlugin(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "..", "plugin"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// install-hooks writes $CODEX_HOME/hooks.json by default, <root>/.codex/hooks.json
// with --repo, any file with --to, and prints with --print; the plugin root is
// --plugin-root, else $PLUGIN_ROOT / $CLAUDE_PLUGIN_ROOT.
func TestCodexInstallHooks(t *testing.T) {
	plugin := repoPlugin(t)
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv("PLUGIN_ROOT", "")
	t.Setenv("CLAUDE_PLUGIN_ROOT", "")

	runRoot(t, "codex", "install-hooks", "--plugin-root", plugin)
	b, err := os.ReadFile(filepath.Join(home, "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `sh \"`+plugin+`/scripts/hook.sh\" session-start codex`) {
		t.Errorf("hooks.json = %s", b)
	}
	runRoot(t, "codex", "install-hooks", "--plugin-root", plugin, "--remove")
	if b, _ := os.ReadFile(filepath.Join(home, "hooks.json")); strings.Contains(string(b), "hook.sh") {
		t.Errorf("after --remove: %s", b)
	}

	repo := t.TempDir()
	t.Setenv("PLUGIN_ROOT", plugin)
	runRoot(t, "codex", "install-hooks", "--repo", "--root", repo)
	if _, err := os.Stat(filepath.Join(repo, ".codex", "hooks.json")); err != nil {
		t.Errorf("--repo: %v", err)
	}

	to := filepath.Join(t.TempDir(), "x.json")
	runRoot(t, "codex", "install-hooks", "--to", to)
	if _, err := os.Stat(to); err != nil {
		t.Errorf("--to: %v", err)
	}

	if out := runRoot(t, "codex", "install-hooks", "--print"); !strings.Contains(out, "UserPromptSubmit") {
		t.Errorf("--print = %q", out)
	}
}

func TestCodexInstallHooksRejects(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	for _, args := range [][]string{
		{"codex", "install-hooks", "--plugin-root", t.TempDir()}, // not a plugin root
		{"codex", "install-hooks", "--plugin-root", repoPlugin(t), "--repo", "--to", "x.json"},
		{"codex", "install-hooks", "--plugin-root", repoPlugin(t), "--print", "--remove"},
	} {
		root := newRootCommand(&options{})
		root.SetOut(&strings.Builder{})
		root.SetErr(&strings.Builder{})
		root.SetArgs(args)
		if err := root.Execute(); err == nil {
			t.Errorf("depgraph %s: want an error", strings.Join(args, " "))
		}
	}
}
