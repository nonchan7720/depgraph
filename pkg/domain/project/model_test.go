package project

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestBaseKey(t *testing.T) {
	root := filepath.FromSlash("/home/u/src/my-repo")
	tests := []struct {
		name, flag, cfg, want string
	}{
		{"flag wins", "explicit", "fromcfg", "explicit"},
		{"config next", "", "fromcfg", "fromcfg"},
		{"dir name by default", "", "", "my-repo"},
		{"blank values are ignored", "  ", " ", "my-repo"},
		{"trimmed", " a ", "", "a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BaseKey(tt.flag, tt.cfg, root); got != tt.want {
				t.Errorf("BaseKey = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestKey(t *testing.T) {
	if got := Key("app", ""); got != "app" {
		t.Errorf("main worktree key = %q", got)
	}
	if got := Key("app", "feat-x"); got != "app@feat-x" {
		t.Errorf("linked worktree key = %q", got)
	}
}

func TestWorktreeName(t *testing.T) {
	root := filepath.FromSlash("/w/app")
	wtRoot := filepath.FromSlash("/w/app-feat")
	tests := []struct {
		name, root, gitDir, commonDir, want string
	}{
		{"main worktree (relative)", root, ".git", ".git", ""},
		{"main worktree subdir", filepath.Join(root, "sub"), filepath.FromSlash("/w/app/.git"), "../.git", ""},
		{"linked worktree (absolute git-dir, absolute common)", wtRoot, filepath.FromSlash("/w/app/.git/worktrees/feat"), filepath.FromSlash("/w/app/.git"), "feat"},
		{"linked worktree (relative common)", wtRoot, filepath.FromSlash("/w/app/.git/worktrees/feat"), "../app/.git", "feat"},
		{"submodule is not a worktree", root, filepath.FromSlash("/w/app/.git/modules/m"), filepath.FromSlash("/w/app/.git/modules/m"), ""},
		{"symlinked path mismatch on main is not a worktree", root, filepath.FromSlash("/private/w/app/.git"), ".git", ""},
		{"no git", root, "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WorktreeName(tt.root, tt.gitDir, tt.commonDir); got != tt.want {
				t.Errorf("WorktreeName = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeOrigin(t *testing.T) {
	tests := map[string]string{
		"":                                       "",
		"  ":                                     "",
		"git@github.com:owner/repo.git":          "github.com/owner/repo",
		"https://github.com/owner/repo.git":      "github.com/owner/repo",
		"https://github.com/owner/repo/":         "github.com/owner/repo",
		"https://user:tok@GitHub.com/owner/repo": "github.com/owner/repo",
		"ssh://git@github.com:22/owner/repo.git": "github.com/owner/repo",
		"ssh://git@github.com/owner/repo\n":      "github.com/owner/repo",
		"/srv/git/repo.git":                      "/srv/git/repo",
		"file:///srv/git/repo.git":               "/srv/git/repo",
	}
	for in, want := range tests {
		if got := NormalizeOrigin(in); got != want {
			t.Errorf("NormalizeOrigin(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReconcile(t *testing.T) {
	last := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	stored := &Project{Key: "app", Base: "app", Root: "/old/app", Origin: "github.com/o/app", LastScanAt: last}
	in := func(root, origin, wt string) Project {
		k := Key("app", wt)
		return Project{Key: k, Base: "app", Worktree: wt, Root: root, Origin: origin}
	}
	exists := func(roots ...string) func(string) bool {
		return func(r string) bool {
			return slices.Contains(roots, r)
		}
	}
	tests := []struct {
		name     string
		existing *Project
		incoming Project
		exists   func(string) bool
		wantRoot string
		conflict bool
	}{
		{"new project", nil, in("/new/app", "", ""), exists(), "/new/app", false},
		{"same root", stored, in("/old/app", "", ""), exists("/old/app"), "/old/app", false},
		{"moved: same origin, old root still there", stored, in("/new/app", "github.com/o/app", ""), exists("/old/app", "/new/app"), "/new/app", false},
		{"moved: old root gone", stored, in("/new/app", "github.com/x/other", ""), exists("/new/app"), "/new/app", false},
		{"moved: old root gone, no git", stored, in("/new/app", "", ""), exists("/new/app"), "/new/app", false},
		{"conflict: different origin, both exist", stored, in("/new/app", "github.com/x/other", ""), exists("/old/app", "/new/app"), "", true},
		{"conflict: no origin on both sides, both exist", &Project{Key: "app", Root: "/old/app"}, in("/new/app", "", ""), exists("/old/app", "/new/app"), "", true},
		{"conflict: worktree name differs even with same origin", stored, Project{Key: "app", Base: "app", Worktree: "feat", Root: "/wt/app", Origin: "github.com/o/app"}, exists("/old/app", "/wt/app"), "", true},
		{"conflict: worktree name differs and old root gone", stored, Project{Key: "app", Base: "app", Worktree: "feat", Root: "/wt/app", Origin: "github.com/o/app"}, exists("/wt/app"), "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Reconcile(tt.existing, tt.incoming, tt.exists)
			if tt.conflict {
				if _, ok := errors.AsType[*ConflictError](err); !ok {
					t.Fatalf("err = %v, want *ConflictError", err)
				}
				msg := err.Error()
				for _, s := range []string{"--project", ".depgraph.yml", tt.existing.Root, tt.incoming.Root} {
					if !strings.Contains(msg, s) {
						t.Errorf("message %q lacks %q", msg, s)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Root != tt.wantRoot || got.Key != tt.incoming.Key || got.Origin != tt.incoming.Origin {
				t.Errorf("Reconcile = %+v", got)
			}
			if tt.existing != nil && !got.LastScanAt.Equal(tt.existing.LastScanAt) {
				t.Errorf("LastScanAt not kept: %v", got.LastScanAt)
			}
		})
	}
}

func TestStale(t *testing.T) {
	ps := []Project{{Key: "a", Root: "/a"}, {Key: "b", Root: "/b"}, {Key: "c", Root: ""}}
	got := Stale(ps, func(r string) bool { return r == "/a" })
	if len(got) != 2 || got[0].Key != "b" || got[1].Key != "c" {
		t.Errorf("Stale = %+v", got)
	}
}

func TestMainRoot(t *testing.T) {
	wt := filepath.FromSlash("/w/app-feat")
	tests := []struct {
		name, root, commonDir, want string
	}{
		{"absolute .git", wt, filepath.FromSlash("/w/app/.git"), filepath.FromSlash("/w/app")},
		{"relative .git", wt, "../app/.git", filepath.FromSlash("/w/app")},
		{"bare repository", wt, filepath.FromSlash("/srv/app.git"), filepath.FromSlash("/srv/app")},
		{"no git", wt, "", wt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MainRoot(tt.root, tt.commonDir); got != tt.want {
				t.Errorf("MainRoot = %q, want %q", got, tt.want)
			}
		})
	}
}
