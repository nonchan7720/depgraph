package hook

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// FileChecker finds the problems one file brings: imports that resolve to
// nothing, imports that close a cycle, ... It reads the file as it is on disk
// now and does not write anything.
type FileChecker interface {
	CheckFile(ctx context.Context, root, rel string) ([]rules.Finding, error)
}

// CheckEdit returns the feedback for the file a tool just edited, or "" when
// the file is outside the project, has no problems, or cannot be checked.
// The project is root, or the hook's cwd when the file lies outside root but
// inside the cwd (a session working in a git worktree of the project).
func CheckEdit(ctx context.Context, in Input, root string, c FileChecker) (string, error) {
	p := in.EditedPath()
	if p == "" || root == "" {
		return "", nil
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	rel, ok := within(root, p)
	if !ok && in.Cwd != "" {
		if cwd, err := filepath.Abs(in.Cwd); err == nil {
			if rel, ok = within(cwd, p); ok {
				root = cwd
			}
		}
	}
	if !ok {
		return "", nil
	}
	findings, err := c.CheckFile(ctx, root, rel)
	if err != nil || len(findings) == 0 {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "depgraph: %s has %d problem(s). Fix them now, before going on:\n", rel, len(findings))
	for _, f := range sortFindings(findings) {
		fmt.Fprintf(&b, "- [%s] %s: %s", f.Severity, f.Rule, f.Message)
		if h := ruleHints[f.Rule]; h != "" {
			fmt.Fprintf(&b, " (%s)", h)
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

// within returns p relative to dir (slash-separated) when p is inside dir.
func within(dir, p string) (string, bool) {
	rel, err := filepath.Rel(dir, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}
