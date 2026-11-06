package imports

import (
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// WorkspacePackage is an npm package of the repository's workspace.
type WorkspacePackage struct {
	Name string // package.json "name"
	Dir  string // slash path from the root
}

// workspaceGlobs returns the member patterns of root's workspace: "workspaces"
// of package.json (npm, yarn, bun: a list or {"packages": [...]}) and
// "packages" of pnpm-workspace.yaml. "!" patterns exclude.
func workspaceGlobs(root string) []string {
	var out []string
	if b, err := os.ReadFile(filepath.Join(root, "package.json")); err == nil {
		var pj struct {
			Workspaces json.RawMessage `json:"workspaces"`
		}
		if json.Unmarshal(b, &pj) == nil && len(pj.Workspaces) > 0 {
			var list []string
			var obj struct {
				Packages []string `json:"packages"`
			}
			if json.Unmarshal(pj.Workspaces, &list) == nil {
				out = append(out, list...)
			} else if json.Unmarshal(pj.Workspaces, &obj) == nil {
				out = append(out, obj.Packages...)
			}
		}
	}
	if b, err := os.ReadFile(filepath.Join(root, "pnpm-workspace.yaml")); err == nil {
		var pw struct {
			Packages []string `yaml:"packages"`
		}
		if yaml.Unmarshal(b, &pw) == nil {
			out = append(out, pw.Packages...)
		}
	}
	return out
}

// workspacePackages returns the named packages of root's workspace, sorted by
// name. node_modules and hidden directories are not searched.
func workspacePackages(root string) []WorkspacePackage {
	var include, exclude []string
	for _, g := range workspaceGlobs(root) {
		g = strings.TrimSpace(g)
		neg := strings.HasPrefix(g, "!")
		g = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(g, "!"), "./"), "/"), "/")
		switch {
		case g == "" || g == ".":
		case neg:
			exclude = append(exclude, g)
		default:
			include = append(include, g)
		}
	}
	if len(include) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []WorkspacePackage
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		rel, ok := toRel(root, p)
		if !ok || rel == "." {
			return nil
		}
		if name := d.Name(); name == "node_modules" || strings.HasPrefix(name, ".") {
			return filepath.SkipDir
		}
		if !matchesAny(include, rel) || matchesAny(exclude, rel) {
			return nil
		}
		b, err := os.ReadFile(filepath.Join(p, "package.json"))
		if err != nil {
			return nil
		}
		var pj struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(b, &pj) != nil || !validPackageName(pj.Name) || seen[pj.Name] {
			return nil
		}
		seen[pj.Name] = true
		out = append(out, WorkspacePackage{Name: pj.Name, Dir: rel})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func matchesAny(globs []string, rel string) bool {
	for _, g := range globs {
		if matchWorkspaceGlob(strings.Split(g, "/"), strings.Split(rel, "/")) {
			return true
		}
	}
	return false
}

// matchWorkspaceGlob matches path segments: ** is any number of segments,
// the rest path.Match within one segment.
func matchWorkspaceGlob(glob, segs []string) bool {
	if len(glob) == 0 {
		return len(segs) == 0
	}
	if glob[0] == "**" {
		for i := 0; i <= len(segs); i++ {
			if matchWorkspaceGlob(glob[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	if ok, err := path.Match(glob[0], segs[0]); err != nil || !ok {
		return false
	}
	return matchWorkspaceGlob(glob[1:], segs[1:])
}

// linkWorkspace creates a directory laid out like node_modules with a link
// to every workspace package of root, for esbuild's NodePaths: it is only
// searched after the node_modules directories, so an installed package of
// the same name still wins. It returns "" when there is nothing to link or
// links cannot be made (a Windows account without the symlink privilege).
func linkWorkspace(root string, pkgs []WorkspacePackage) string {
	if len(pkgs) == 0 {
		return ""
	}
	dir, err := os.MkdirTemp("", "depgraph-workspace-")
	if err != nil {
		return ""
	}
	for _, p := range pkgs {
		link := filepath.Join(dir, filepath.FromSlash(p.Name))
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			_ = os.RemoveAll(dir)
			return ""
		}
		if err := os.Symlink(filepath.Join(root, filepath.FromSlash(p.Dir)), link); err != nil {
			_ = os.RemoveAll(dir)
			return ""
		}
	}
	return dir
}
