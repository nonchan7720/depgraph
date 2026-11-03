package hook

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

type fakeChecker struct {
	got      []string
	findings []rules.Finding
	err      error
}

func (f *fakeChecker) CheckFile(_ context.Context, root, rel string) ([]rules.Finding, error) {
	f.got = append(f.got, root+"|"+rel)
	return f.findings, f.err
}

func TestParsePostEdit(t *testing.T) {
	if ev, err := ParseEvent("post-edit"); err != nil || ev != PostEdit {
		t.Fatalf("ParseEvent = %q, %v", ev, err)
	}
	in, err := ParseInput(strings.NewReader(`{"session_id":"s","cwd":"/r","hook_event_name":"PostToolUse",
		"tool_name":"Edit","tool_input":{"file_path":"/r/src/a.ts","old_string":"x"}}`))
	if err != nil || in.ToolName != "Edit" || in.ToolInput.FilePath != "/r/src/a.ts" {
		t.Fatalf("ParseInput = %+v, %v", in, err)
	}
	in, err = ParseInput(strings.NewReader(`{"tool_name":"NotebookEdit","tool_input":{"notebook_path":"/r/n.ipynb"}}`))
	if err != nil || in.EditedPath() != "/r/n.ipynb" {
		t.Fatalf("notebook = %+v, %v", in, err)
	}
}

func TestCheckEdit(t *testing.T) {
	root := t.TempDir()
	in := func(p string) Input { return Input{ToolName: "Write", ToolInput: ToolInput{FilePath: p}} }
	ctx := context.Background()

	// problems in the edited file are fed back with what to do
	c := &fakeChecker{findings: []rules.Finding{
		rules.UnresolvedImport("src/a.ts", "@/x", ""),
		rules.ImportCycle([]string{"src", "lib", "src"}),
	}}
	out, err := CheckEdit(ctx, in(filepath.Join(root, "src", "a.ts")), root, c)
	if err != nil || len(c.got) != 1 || c.got[0] != root+"|src/a.ts" {
		t.Fatalf("checked %v, err %v", c.got, err)
	}
	for _, want := range []string{"src/a.ts", `"@/x"`, "lib -> src -> lib", "[error] import-cycle", "[warn] unresolved-import"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Index(out, "import-cycle") > strings.Index(out, "unresolved-import") {
		t.Errorf("errors first:\n%s", out)
	}

	// outside root but inside the hook's cwd (a git worktree of the project):
	// checked against the cwd
	wt := t.TempDir()
	c = &fakeChecker{}
	edit := in(filepath.Join(wt, "src", "c.ts"))
	edit.Cwd = wt
	if _, err := CheckEdit(ctx, edit, root, c); err != nil || len(c.got) != 1 || c.got[0] != wt+"|src/c.ts" {
		t.Errorf("cwd root: checked %v, err %v", c.got, err)
	}

	// a relative path is taken from the root
	c = &fakeChecker{}
	if out, _ := CheckEdit(ctx, in("src/b.ts"), root, c); out != "" || c.got[0] != root+"|src/b.ts" {
		t.Errorf("relative path: out %q, checked %v", out, c.got)
	}
	// clean file, a file outside the root, no path, a checker error: nothing to say
	for name, tc := range map[string]struct {
		in Input
		c  *fakeChecker
	}{
		"clean":   {in(filepath.Join(root, "a.ts")), &fakeChecker{}},
		"outside": {in(filepath.Join(filepath.Dir(root), "other", "a.ts")), &fakeChecker{findings: []rules.Finding{rules.ImportCycle([]string{"a", "a"})}}},
		"no path": {Input{ToolName: "Bash"}, &fakeChecker{findings: []rules.Finding{rules.ImportCycle([]string{"a", "a"})}}},
		"error":   {in(filepath.Join(root, "a.ts")), &fakeChecker{err: errors.New("boom")}},
	} {
		out, _ := CheckEdit(ctx, tc.in, root, tc.c)
		if out != "" {
			t.Errorf("%s: %q", name, out)
		}
		if name == "outside" || name == "no path" {
			if len(tc.c.got) != 0 {
				t.Errorf("%s: checked %v", name, tc.c.got)
			}
		}
	}
}

// PostToolUse feedback is a "block" decision: Claude Code shows the reason
// to Claude right after the edit (the edit itself is not undone).
func TestOutputPostEdit(t *testing.T) {
	if b := Output(PostEdit, ""); b != nil {
		t.Errorf("empty: %s", b)
	}
	var got map[string]any
	if err := json.Unmarshal(Output(PostEdit, "fix a -> b"), &got); err != nil {
		t.Fatal(err)
	}
	hso, _ := got["hookSpecificOutput"].(map[string]any)
	if got["decision"] != "block" || got["reason"] != "fix a -> b" ||
		hso["hookEventName"] != "PostToolUse" || hso["additionalContext"] != "fix a -> b" {
		t.Errorf("output = %v", got)
	}
}
