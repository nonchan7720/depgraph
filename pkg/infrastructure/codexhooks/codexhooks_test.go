package codexhooks

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const template = `{
  "hooks": {
    "SessionStart": [
      { "hooks": [ { "type": "command", "timeout": 120, "command": "sh \"${PLUGIN_ROOT}/scripts/hook.sh\" session-start codex" } ] }
    ],
    "UserPromptSubmit": [
      { "hooks": [ { "type": "command", "timeout": 10, "command": "sh \"${PLUGIN_ROOT}/scripts/hook.sh\" prompt codex" } ] }
    ]
  }
}
`

// pluginRoot writes a plugin root with the Codex hooks template.
func pluginRoot(t *testing.T) string {
	t.Helper()
	return pluginRootAt(t, filepath.Join(t.TempDir(), "plugin root"))
}

func pluginRootAt(t *testing.T, root string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(Template)), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

type hooksFile struct {
	Hooks map[string][]struct {
		Matcher string `json:"matcher"`
		Hooks   []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
			Timeout int    `json:"timeout"`
		} `json:"hooks"`
	} `json:"hooks"`
}

func decode(t *testing.T, b []byte) hooksFile {
	t.Helper()
	var f hooksFile
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	return f
}

func commands(f hooksFile, event string) []string {
	var out []string
	for _, g := range f.Hooks[event] {
		for _, h := range g.Hooks {
			out = append(out, h.Command)
		}
	}
	return out
}

func TestInstallIntoNewFile(t *testing.T) {
	root := pluginRoot(t)
	target := filepath.Join(t.TempDir(), "codex", "hooks.json")
	if err := Install(Options{Root: root, Target: target}, nil); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	f := decode(t, b)
	want := `sh "` + root + `/scripts/hook.sh" session-start codex`
	if got := commands(f, "SessionStart"); len(got) != 1 || got[0] != want {
		t.Errorf("SessionStart = %q, want %q", got, want)
	}
	if got := commands(f, "UserPromptSubmit"); len(got) != 1 || !strings.HasSuffix(got[0], `/scripts/hook.sh" prompt codex`) {
		t.Errorf("UserPromptSubmit = %q", got)
	}
	if f.Hooks["SessionStart"][0].Hooks[0].Timeout != 120 {
		t.Errorf("timeout lost: %s", b)
	}
	if strings.Contains(string(b), "\\u0026") || strings.Contains(string(b), "${PLUGIN_ROOT}") {
		t.Errorf("output = %s", b)
	}
}

// Other hooks and keys of the target stay, in their order; running it again
// replaces the plugin's own entries instead of adding a second set (also
// entries of an older install pointing at another root).
func TestInstallMergesAndIsIdempotent(t *testing.T) {
	root := pluginRoot(t)
	target := filepath.Join(t.TempDir(), "hooks.json")
	existing := `{
  "description": "mine",
  "hooks": {
    "Stop": [ { "hooks": [ { "type": "command", "command": "echo stop && true" } ] } ],
    "SessionStart": [
      { "matcher": "startup", "hooks": [ { "type": "command", "command": "other-tool start" } ] },
      { "hooks": [ { "type": "command", "command": "sh \"/old/depgraph/0.1.0/scripts/hook.sh\" session-start codex" } ] }
    ]
  },
  "zzz": 1
}
`
	if err := os.WriteFile(target, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := Install(Options{Root: root, Target: target}, nil); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(target)
	f := decode(t, b)
	ss := commands(f, "SessionStart")
	if len(ss) != 2 || ss[0] != "other-tool start" || ss[1] != `sh "`+root+`/scripts/hook.sh" session-start codex` {
		t.Errorf("SessionStart = %q", ss)
	}
	if f.Hooks["SessionStart"][0].Matcher != "startup" {
		t.Errorf("matcher lost: %s", b)
	}
	if got := commands(f, "Stop"); len(got) != 1 || got[0] != "echo stop && true" {
		t.Errorf("Stop = %q", got)
	}
	if len(commands(f, "UserPromptSubmit")) != 1 {
		t.Errorf("UserPromptSubmit = %q", commands(f, "UserPromptSubmit"))
	}
	s := string(b)
	if !inOrder(s, `"description"`, `"hooks"`, `"zzz"`) || !inOrder(s, `"Stop"`, `"SessionStart"`, `"UserPromptSubmit"`) {
		t.Errorf("key order changed:\n%s", s)
	}
	if strings.Contains(s, "\\u0026") {
		t.Errorf("HTML-escaped: %s", s)
	}
	if fi, err := os.Stat(target); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v %v", fi.Mode(), err)
	}
}

// inOrder reports whether every key occurs in s, in this order.
func inOrder(s string, keys ...string) bool {
	last := -1
	for _, k := range keys {
		i := strings.Index(s, k)
		if i <= last {
			return false
		}
		last = i
	}
	return true
}

func TestRemove(t *testing.T) {
	root := pluginRoot(t)
	target := filepath.Join(t.TempDir(), "hooks.json")
	existing := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"keep"}]}]}}`
	if err := os.WriteFile(target, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Install(Options{Root: root, Target: target}, nil); err != nil {
		t.Fatal(err)
	}
	if err := Install(Options{Root: root, Target: target, Remove: true}, nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(target)
	f := decode(t, b)
	if len(f.Hooks) != 1 || len(commands(f, "Stop")) != 1 {
		t.Errorf("after remove: %s", b)
	}
	// removing from a file that does not exist writes nothing
	none := filepath.Join(t.TempDir(), "hooks.json")
	if err := Install(Options{Root: root, Target: none, Remove: true}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(none); !os.IsNotExist(err) {
		t.Errorf("remove created %s", none)
	}
}

// --print writes nothing: alone it prints the hooks, with a target it prints
// the target merged with them (for a file only root may write).
func TestPrint(t *testing.T) {
	root := pluginRoot(t)
	var out bytes.Buffer
	if err := Install(Options{Root: root, Print: true}, &out); err != nil {
		t.Fatal(err)
	}
	if got := commands(decode(t, out.Bytes()), "SessionStart"); len(got) != 1 {
		t.Errorf("print = %s", out.String())
	}

	target := filepath.Join(t.TempDir(), "hooks.json")
	existing := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"keep"}]}]}}`
	if err := os.WriteFile(target, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Install(Options{Root: root, Target: target, Print: true}, &out); err != nil {
		t.Fatal(err)
	}
	f := decode(t, out.Bytes())
	if len(commands(f, "Stop")) != 1 || len(commands(f, "SessionStart")) != 1 {
		t.Errorf("print --to = %s", out.String())
	}
	if b, _ := os.ReadFile(target); string(b) != existing {
		t.Errorf("print wrote the target: %s", b)
	}
	if err := Install(Options{Root: root, Print: true, Remove: true}, &out); err == nil {
		t.Error("print --remove without a target: want an error")
	}
}

func TestErrors(t *testing.T) {
	root := pluginRoot(t)
	target := filepath.Join(t.TempDir(), "hooks.json")
	if err := Install(Options{Root: t.TempDir(), Target: target}, nil); err == nil || !strings.Contains(err.Error(), Template) {
		t.Errorf("not a plugin root: %v", err)
	}
	if err := os.WriteFile(target, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Install(Options{Root: root, Target: target}, nil); err == nil {
		t.Error("invalid target: want an error")
	}
	if b, _ := os.ReadFile(target); string(b) != "{not json" {
		t.Errorf("invalid target was rewritten: %s", b)
	}
	if err := os.WriteFile(target, []byte(`{"hooks":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Install(Options{Root: root, Target: target}, nil); err == nil {
		t.Error(`"hooks" not an object: want an error`)
	}
	// the root ends up inside a double-quoted shell word
	for _, bad := range []string{`a"b`, `a$b`, "a`b", `a\b`, "a\nb"} {
		dir := pluginRootAt(t, filepath.Join(t.TempDir(), bad))
		if err := Install(Options{Root: dir, Print: true}, &bytes.Buffer{}); err == nil {
			t.Errorf("root %q: want an error", bad)
		}
	}
}

func TestIsPluginCommand(t *testing.T) {
	for cmd, want := range map[string]bool{
		`sh "${PLUGIN_ROOT}/scripts/hook.sh" session-start codex`:                                true,
		`sh "/home/u/.codex/plugins/cache/depgraph/depgraph/0.2.0/scripts/hook.sh" prompt codex`: true,
		`sh "${CLAUDE_PLUGIN_ROOT}/scripts/hook.sh" session-start`:                               false, // the Claude Code plugin's
		`depgraph hook prompt`:                                 false,
		`bash "/x/plugins/codex/scripts/hook.sh" SessionStart`: false,
	} {
		if got := IsPluginCommand(cmd); got != want {
			t.Errorf("IsPluginCommand(%q) = %v", cmd, got)
		}
	}
}
