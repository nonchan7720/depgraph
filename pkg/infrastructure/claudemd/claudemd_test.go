package claudemd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	"github.com/nonchan7720/depgraph/pkg/domain/view"
)

func TestUpdatePreservesHandwrittenContent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "CLAUDE.md")

	// 1. file does not exist -> created with just the section
	if err := Update(p, "first\n"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "first") {
		t.Fatalf("got %q", b)
	}

	// 2. user adds content around it
	if err := os.WriteFile(p, []byte("# My project\n\nhand written\n\n"+string(b)+"\nmore hand written\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 3. update replaces only the managed section
	if err := Update(p, "second\n"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(p)
	s := string(b)
	for _, want := range []string{"# My project", "hand written", "more hand written", "second"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "first") {
		t.Errorf("old section still present:\n%s", s)
	}
	if strings.Count(s, startMarker) != 1 || strings.Count(s, endMarker) != 1 {
		t.Errorf("markers duplicated:\n%s", s)
	}
}

func TestWriter(t *testing.T) {
	p := filepath.Join(t.TempDir(), "CLAUDE.md")
	w := Writer{Path: p}
	err := w.Write(view.Stats{Packages: 3}, []rules.Finding{{Rule: "layer-violation", Severity: "error", Message: "a -> b"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "a -> b") || !strings.Contains(string(b), startMarker) {
		t.Errorf("managed section not written: %s", b)
	}
}

// Every finding reaches CLAUDE.md: rules without a fixed title (new or
// configured ones) are listed after the known ones, not dropped.
func TestRenderListsEveryRule(t *testing.T) {
	out := Render(view.Stats{}, []rules.Finding{
		{Rule: "zz-custom", Severity: "warn", Message: "custom finding"},
		{Rule: "god-package", Severity: "warn", Message: "pkg/x is imported by 12 packages"},
		{Rule: "import-cycle", Severity: "error", Message: "a -> b -> a"},
	})
	for _, want := range []string{"### God packages", "pkg/x is imported by 12 packages", "### zz-custom", "custom finding", "a -> b -> a"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if i, j := strings.Index(out, "Import cycles"), strings.Index(out, "### zz-custom"); i < 0 || j < i {
		t.Errorf("known rules come first:\n%s", out)
	}
}

// The unused-code rules have titles, and a long list is cut (all of them are
// in `depgraph check`): CLAUDE.md is read by every prompt.
func TestRenderUnusedRules(t *testing.T) {
	fs := []rules.Finding{
		rules.UnreachableFile("src/old.ts", nil, false),
		rules.UnreachablePackage("pkg/old", nil, false),
	}
	for i := range 40 {
		fs = append(fs, rules.UnusedExport("src/a.ts", i+1, "function", fmt.Sprintf("f%02d", i)))
	}
	out := Render(view.Stats{}, fs)
	for _, want := range []string{"### Unused exports", "### Unreachable files", "### Unreachable Go packages", "src/old.ts", "f19", "20 more"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "f20") {
		t.Errorf("list not cut:\n%s", out)
	}
}

func TestRenderDuplicateCode(t *testing.T) {
	out := Render(view.Stats{}, []rules.Finding{{Rule: "duplicate-code", Severity: "warn", Message: "identical logic in 2 places: a.go:1-9 A, b.go:1-9 B"}})
	if !strings.Contains(out, "### Duplicated code") || !strings.Contains(out, "a.go:1-9 A") {
		t.Errorf("render:\n%s", out)
	}
}

// Every built-in rule has a title that tells the assistant what to do.
func TestRenderTitlesEveryBuiltinRule(t *testing.T) {
	for _, r := range rules.Builtin() {
		out := Render(view.Stats{}, []rules.Finding{{Rule: r.Name, Severity: string(r.Severity), Message: "m"}})
		if strings.Contains(out, "### "+r.Name+"\n") {
			t.Errorf("%s has no title:\n%s", r.Name, out)
		}
	}
}
