package hook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	"github.com/nonchan7720/depgraph/pkg/domain/view"
)

func f(rule, sev, msg string) rules.Finding {
	return rules.Finding{Rule: rule, Severity: sev, Message: msg}
}

// ---- stdin / root ------------------------------------------------------------

func TestParseInput(t *testing.T) {
	in, err := ParseInput(strings.NewReader(`{"session_id":"s1","cwd":"/repo","hook_event_name":"UserPromptSubmit","prompt":"hi","transcript_path":"/x","unknown":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if in.SessionID != "s1" || in.Cwd != "/repo" || in.HookEventName != "UserPromptSubmit" || in.Prompt != "hi" {
		t.Errorf("ParseInput = %+v", in)
	}
	if _, err := ParseInput(strings.NewReader("not json")); err == nil {
		t.Error("want error for invalid JSON")
	}
	// 空入力はエラーにせずゼロ値（手動実行時など）。
	if in, err := ParseInput(strings.NewReader("")); err != nil || in != (Input{}) {
		t.Errorf("empty input = %+v, %v", in, err)
	}
}

func TestResolveRoot(t *testing.T) {
	tests := []struct{ flag, env, cwd, want string }{
		{"/flag", "/env", "/cwd", "/flag"},
		{"", "/env", "/cwd", "/env"},
		{"", "", "/cwd", "/cwd"},
		{"", "", "", ""},
		{"/a/b/../c/", "", "", "/a/c"},
	}
	for _, tt := range tests {
		if got := ResolveRoot(tt.flag, tt.env, tt.cwd); got != filepath.FromSlash(tt.want) {
			t.Errorf("ResolveRoot(%q,%q,%q) = %q, want %q", tt.flag, tt.env, tt.cwd, got, tt.want)
		}
	}
	// 相対パスは絶対パスになる。
	if got := ResolveRoot("rel", "", ""); !filepath.IsAbs(got) {
		t.Errorf("relative root not absolute: %q", got)
	}
}

// ---- summary -------------------------------------------------------------------

func TestSummarizeOrdersBySeverityAndCompresses(t *testing.T) {
	var fs []rules.Finding
	for i := range 50 {
		fs = append(fs, f("duplicate-symbol", "warn", fmt.Sprintf("dup%02d in a.go, b.go", i)))
	}
	fs = append(fs, f("god-package", "warn", "pkg/x imported by 12"), f("import-cycle", "error", "a -> b -> a"))
	out := Summarize(view.Stats{Packages: 3, Files: 9, Symbols: 40, Imports: 5}, fs, DefaultLimits())

	for _, want := range []string{"3 packages, 9 files, 40 symbols", "1 error, 51 warn", "a -> b -> a", "duplicate-symbol (50", "and 45 more"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary lacks %q:\n%s", want, out)
		}
	}
	// error が先、その後 warn のルール。
	ic, gp, ds := strings.Index(out, "import-cycle"), strings.Index(out, "god-package"), strings.Index(out, "duplicate-symbol")
	if ic < 0 || ic >= gp || gp >= ds {
		t.Errorf("order import-cycle(%d) < god-package(%d) < duplicate-symbol(%d) expected:\n%s", ic, gp, ds, out)
	}
	// 代表例は 5 件だけ（dup00..dup04）。
	if !strings.Contains(out, "dup04") || strings.Contains(out, "dup05") {
		t.Errorf("want only 5 examples of duplicate-symbol:\n%s", out)
	}
}

func TestSummarizeCapsLength(t *testing.T) {
	var fs []rules.Finding
	long := strings.Repeat("x", 300)
	for r := range 40 { // 40 ルール × 5 例 × 300 文字 ≫ 上限
		for i := range 6 {
			fs = append(fs, f(fmt.Sprintf("rule-%02d", r), "warn", fmt.Sprintf("%d %s", i, long)))
		}
	}
	lim := DefaultLimits()
	out := Summarize(view.Stats{}, fs, lim)
	if len(out) > lim.MaxChars {
		t.Errorf("len = %d, want <= %d", len(out), lim.MaxChars)
	}
	if !strings.Contains(out, "truncated") {
		t.Errorf("want truncation note:\n%s", out[len(out)-200:])
	}
}

// The unused-code rules come after the architecture rules, with a hint.
func TestSummarizeUnusedRules(t *testing.T) {
	out := Summarize(view.Stats{}, []rules.Finding{
		rules.UnusedExport("a.ts", 1, "function", "f"),
		rules.UnreachableFile("old.ts", nil, false),
		f("duplicate-symbol", "warn", "dup"),
	}, DefaultLimits())
	ds, ue, uf := strings.Index(out, "duplicate-symbol"), strings.Index(out, "unused-export"), strings.Index(out, "unreachable-file")
	if ds < 0 || ds > ue || ue > uf {
		t.Errorf("order duplicate-symbol < unused-export < unreachable-file expected:\n%s", out)
	}
	if !strings.Contains(out, "delete them instead of building on them") {
		t.Errorf("no hint for unused-export:\n%s", out)
	}
}

func TestSummarizeNoFindings(t *testing.T) {
	out := Summarize(view.Stats{Packages: 1}, nil, DefaultLimits())
	if !strings.Contains(out, "No architectural violations") {
		t.Errorf("summary = %q", out)
	}
}

func TestRenderDiff(t *testing.T) {
	cur := []rules.Finding{f("import-cycle", "error", "a -> b -> a"), f("god-package", "warn", "x")}
	added := []rules.Finding{f("god-package", "warn", "x"), f("import-cycle", "error", "a -> b -> a")}
	removed := []rules.Finding{f("duplicate-symbol", "warn", "Helper in a.go, b.go")}
	out := RenderDiff(added, removed, cur, DefaultLimits())
	for _, want := range []string{"New (2)", "Resolved (1)", "[error] import-cycle: a -> b -> a", "Helper in a.go, b.go", "1 error, 1 warn"} {
		if !strings.Contains(out, want) {
			t.Errorf("diff lacks %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "import-cycle") > strings.Index(out, "god-package") {
		t.Errorf("errors must come first:\n%s", out)
	}
	if RenderDiff(nil, nil, cur, DefaultLimits()) != "" {
		t.Error("no change must render nothing")
	}

	var many []rules.Finding
	for i := range 30 {
		many = append(many, f("duplicate-symbol", "warn", fmt.Sprintf("d%02d", i)))
	}
	out = RenderDiff(many, nil, many, DefaultLimits())
	if !strings.Contains(out, "and 10 more") || strings.Contains(out, "d20") {
		t.Errorf("want 20 items and a remainder line:\n%s", out)
	}
}

// ---- state ---------------------------------------------------------------------

// ---- output --------------------------------------------------------------------

func TestOutput(t *testing.T) {
	if Output(SessionStart, "") != nil {
		t.Error("empty context must print nothing")
	}
	var v struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(Output(Prompt, "ctx"), &v); err != nil {
		t.Fatal(err)
	}
	if v.HookSpecificOutput.HookEventName != "UserPromptSubmit" || v.HookSpecificOutput.AdditionalContext != "ctx" {
		t.Errorf("output = %+v", v)
	}
	if strings.Contains(string(Output(Prompt, "a -> b")), `\u003e`) {
		t.Error("arrows must not be HTML-escaped")
	}
	if !strings.Contains(string(Output(SessionStart, "x")), `"SessionStart"`) {
		t.Error("session-start event name")
	}
}

// ---- Run -----------------------------------------------------------------------

// fakeSessions is an in-memory SessionStore.
type fakeSessions struct {
	m    map[string][]rules.Finding
	base map[string][]rules.Finding
	puts int
}

func (s *fakeSessions) Baseline(_ context.Context, id string) ([]rules.Finding, bool, error) {
	fs, ok := s.base[id]
	return fs, ok, nil
}

func (s *fakeSessions) PutBaseline(_ context.Context, id string, fs []rules.Finding, _ time.Time) error {
	if s.base == nil {
		s.base = map[string][]rules.Finding{}
	}
	if _, ok := s.base[id]; !ok {
		s.base[id] = fs
	}
	return nil
}

func (s *fakeSessions) Get(_ context.Context, id string) ([]rules.Finding, bool, error) {
	fs, ok := s.m[id]
	return fs, ok, nil
}

func (s *fakeSessions) Put(_ context.Context, id string, fs []rules.Finding, _ time.Time) error {
	if s.m == nil {
		s.m = map[string][]rules.Finding{}
	}
	s.m[id] = fs
	s.puts++
	return nil
}

// fakeProject は root ごとのデータを持つ ProjectData。
type fakeProject struct {
	data     map[string][]rules.Finding // root -> findings（キーが無ければデータ無し）
	err      error
	loads    int
	sessions fakeSessions
}

func (p *fakeProject) Load(_ context.Context, root string) (Snapshot, bool, error) {
	p.loads++
	if p.err != nil {
		return Snapshot{}, false, p.err
	}
	fs, ok := p.data[root]
	if !ok {
		return Snapshot{}, false, nil
	}
	return Snapshot{Stats: view.Stats{Packages: 2}, Findings: fs, Sessions: &p.sessions}, true, nil
}

func TestRunSessionStartThenPrompt(t *testing.T) {
	root := t.TempDir()
	src := &fakeProject{data: map[string][]rules.Finding{root: {f("god-package", "warn", "x")}}}
	ctx, now := context.Background(), time.Now()
	in := Input{SessionID: "s1"}

	out, err := Run(ctx, SessionStart, in, root, src, now)
	if err != nil || !strings.Contains(out, "god-package") {
		t.Fatalf("session-start = %q, %v", out, err)
	}
	// 変化なし → 何も出さない。
	if out, err := Run(ctx, Prompt, in, root, src, now); err != nil || out != "" {
		t.Fatalf("prompt without change = %q, %v", out, err)
	}
	// 新規と解消。
	src.data[root] = []rules.Finding{f("import-cycle", "error", "a -> b -> a")}
	out, err = Run(ctx, Prompt, in, root, src, now)
	if err != nil || !strings.Contains(out, "New (1)") || !strings.Contains(out, "Resolved (1)") {
		t.Fatalf("prompt with change = %q, %v", out, err)
	}
	// 一度伝えた差分は繰り返さない。
	if out, _ := Run(ctx, Prompt, in, root, src, now); out != "" {
		t.Fatalf("diff repeated: %q", out)
	}
	// 別セッションは独立（状態が無ければ全体の要約）。
	out, _ = Run(ctx, Prompt, Input{SessionID: "s2"}, root, src, now)
	if !strings.Contains(out, "import-cycle") || strings.Contains(out, "Resolved") {
		t.Fatalf("prompt for a new session = %q", out)
	}
}

// The hook writes nothing into the repository: session state lives in the
// store. The state file of earlier versions is removed.
func TestRunWritesNothingToTheRepository(t *testing.T) {
	root := t.TempDir()
	src := &fakeProject{data: map[string][]rules.Finding{root: {f("god-package", "warn", "x")}}}
	if _, err := Run(context.Background(), SessionStart, Input{SessionID: "s1"}, root, src, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".claude")); !os.IsNotExist(err) {
		t.Errorf("nothing may be written under the root: %v", err)
	}
	if src.sessions.puts != 1 {
		t.Errorf("session state stored %d times", src.sessions.puts)
	}

	legacy := filepath.Join(root, ".claude", ".depgraph-hook-state.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(`{"sessions":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(root, ".claude", "settings.json")
	if err := os.WriteFile(keep, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), Prompt, Input{SessionID: "s1"}, root, src, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("legacy state file must be removed: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("other files must stay: %v", err)
	}
}

func TestRunInjectsNothingWithoutProjectData(t *testing.T) {
	root := t.TempDir()
	// DB に入っているのは別プロジェクトのデータだけ。
	src := &fakeProject{data: map[string][]rules.Finding{"/other/project": {f("import-cycle", "error", "a")}}}
	for _, ev := range []Event{SessionStart, Prompt} {
		out, err := Run(context.Background(), ev, Input{SessionID: "s"}, root, src, time.Now())
		if err != nil || out != "" {
			t.Errorf("%s: out=%q err=%v", ev, out, err)
		}
	}
	if src.sessions.puts != 0 {
		t.Errorf("no session state without project data: %d puts", src.sessions.puts)
	}
	// root が決まらないときは問い合わせもしない。
	src.loads = 0
	if out, _ := Run(context.Background(), SessionStart, Input{SessionID: "s"}, "", src, time.Now()); out != "" || src.loads != 0 {
		t.Errorf("empty root: out=%q loads=%d", out, src.loads)
	}
	src.err = errors.New("down")
	if _, err := Run(context.Background(), Prompt, Input{SessionID: "s"}, root, src, time.Now()); err == nil {
		t.Error("source error must be returned (the command turns it into no output)")
	}
}

func TestRunPromptWithoutSessionID(t *testing.T) {
	root := t.TempDir()
	src := &fakeProject{data: map[string][]rules.Finding{root: {f("r", "warn", "m")}}}
	if out, _ := Run(context.Background(), Prompt, Input{}, root, src, time.Now()); out != "" {
		t.Errorf("prompt without session_id = %q", out)
	}
	if src.sessions.puts != 0 {
		t.Error("state must not be written without session_id")
	}
}

func TestParseEvent(t *testing.T) {
	for in, want := range map[string]Event{"session-start": SessionStart, "prompt": Prompt} {
		if got, err := ParseEvent(in); err != nil || got != want {
			t.Errorf("ParseEvent(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseEvent("stop"); err == nil {
		t.Error("want error for unknown event")
	}
}

func TestSummarizeDuplicateCodeHint(t *testing.T) {
	out := Summarize(view.Stats{}, []rules.Finding{{Rule: "duplicate-code", Severity: "warn", Message: "identical logic in 2 places"}}, DefaultLimits())
	if !strings.Contains(out, "extract and reuse") {
		t.Errorf("summary:\n%s", out)
	}
}
