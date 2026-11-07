package imports

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

func TestPlanPythonVersions(t *testing.T) {
	root := tree(t, map[string]string{
		".python-version":                "3.12\n",
		"mise.toml":                      "[tools]\npython = \"3.12.4\"\n", // agrees with .python-version
		"app/main.py":                    "",
		"lib/foo/.python-version":        "3.9\n", // a library kept on an older version
		"lib/foo/pyproject.toml":         "[project]\nrequires-python = \">=3.9\"\n",
		"lib/foo/foo/core.py":            "",
		"svc/a/Dockerfile":               "FROM python:3.11-slim\n",
		"svc/a/app.py":                   "",
		"svc/b/.python-version":          "3.10\n",
		"svc/b/Dockerfile":               "FROM python:3.11\n", // disagrees: not settled here
		"svc/b/b.py":                     "",
		"docs/.python-version":           "3.8\n", // no Python file under it
		"web/package.json":               "{}",
		"node_modules/x/.python-version": "2.7\n", // skipped directory
		"done/.python-version":           "3.13\n",
		"done/d.py":                      "",
	})
	cfg := rules.DefaultConfig()
	cfg.Languages = []rules.LanguageSetting{{Dir: "done", Lang: "python", Version: "3.13"}}
	plan := PlanPythonVersions(root, cfg)
	var got []string
	for _, e := range plan.Entries {
		got = append(got, e.Dir+"="+e.VersionFile)
	}
	want := []string{".=.python-version", "lib/foo=.python-version", "svc/a=Dockerfile"}
	if !slices.Equal(got, want) {
		t.Errorf("entries = %v, want %v", got, want)
	}
	if len(plan.Undecided) != 1 || plan.Undecided[0].Dir != "svc/b" || len(plan.Undecided[0].Candidates) != 2 {
		t.Fatalf("undecided = %+v", plan.Undecided)
	}
	for _, c := range plan.Undecided[0].Candidates {
		if c.File == "" || c.Version == "" {
			t.Errorf("candidate %+v", c)
		}
	}
}

// A symlink that leaves the repository works here, but the gate's
// worktrees do not have its target: init says so.
func TestPlanPythonVersionsOutsideSymlink(t *testing.T) {
	root := tree(t, map[string]string{"a.py": ""})
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, ".python-version"), []byte("3.12\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, ".python-version"), filepath.Join(root, ".python-version")); err != nil {
		t.Fatal(err)
	}
	plan := PlanPythonVersions(root, rules.DefaultConfig())
	if len(plan.Entries) != 1 || !strings.Contains(strings.Join(plan.Notes, "\n"), "outside the repository") {
		t.Errorf("plan = %+v", plan)
	}
	for _, n := range plan.Notes {
		if strings.Contains(n, outside) {
			t.Errorf("a note shows the real path: %s", n)
		}
	}
}
