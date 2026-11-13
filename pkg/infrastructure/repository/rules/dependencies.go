package rules

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver/deps"
)

// The dependencies the manifests declare (go.mod, package.json,
// pyproject.toml / requirements files, Gemfile), in the linked graph:
//
//	(Dependency {key, manifest, dir, eco, name, section, line, languages,
//	             constants, checked, use, doubt})
//	(Dependency)-[:PROVIDES {scopes}]->(Package)
//
// A Dependency is one declaration of one manifest. PROVIDES connects it to
// the external packages it provides, for the files of scopes (File.scope:
// the directory of the manifest that governs a file): an import uses the
// declaration of the nearest manifest that names the package (deps.Index),
// and an import of a Go package belongs to the required module with the
// longest path. constants are the top-level constants of a gem (Rails
// code uses a gem through them, not through a require). Whether such a file
// imports the package or refers to such a constant is for the rule to say.
//
// For a dependency no such file imports, linking reads what else the
// project's files say (deps.Fact): use, how it is used without an import (a
// file names it, a peer, a go.mod tool); doubt, why a use cannot be ruled
// out. checked marks the dependencies those facts were read for.
//
// The facts are read from files that are not in the graph (configurations,
// scripts, node_modules), so they are as of the last link: a manifest that
// changed, packages that were installed and any change of a source file
// link again; a change of another file alone does not.

// dependencies are the manifests of a project.
type dependencies struct {
	root      string
	manifests []deps.Manifest
}

// dependencies reads the project's manifests: of the root and of its
// modules. nil when the rule is off or the project's files are not
// available here.
func (r *Repository) dependencies(cfg rules.Config) *dependencies {
	if r.root == "" || !cfg.Enabled(rules.RuleUnusedDependency) {
		return nil
	}
	return &dependencies{root: r.root, manifests: deps.Declared(r.root, cfg.Modules)}
}

// signature identifies what the manifests declare (the link is redone when
// one changed).
func (d *dependencies) signature() string {
	if d == nil {
		return ""
	}
	return deps.Signature(d.root, d.manifests)
}

// link adds the dependencies to the linked graph.
func (d *dependencies) link(ctx context.Context, r *Repository, want *linked) error {
	if d == nil || len(d.manifests) == 0 {
		return nil
	}
	rows, err := r.query(ctx, `
		MATCH (f:File {project: $project})-[:IMPORTS]->(p:Package {project: $project})
		WHERE p.external = true
		RETURN DISTINCT coalesce(f.scope, '.') AS scope, f.language AS lang, p.key AS key
	`, nil, nil)
	if err != nil {
		return fmt.Errorf("dependencies: %w", err)
	}
	type scopeKey struct{ scope, key string }
	uses := map[deps.Ref][]scopeKey{} // dependency -> the imports that use it
	ix := deps.NewIndex(d.manifests)
	for _, row := range rows {
		scope, key := str(row["scope"]), str(row["key"])
		for _, ref := range ix.Users(str(row["lang"]), scope, key) {
			uses[ref] = append(uses[ref], scopeKey{scope, key})
		}
	}
	consts, err := r.rubyConstants(ctx)
	if err != nil {
		return fmt.Errorf("dependencies: %w", err)
	}
	open := map[string][]string{} // manifest -> its dependencies nothing imports or refers to
	for _, m := range d.manifests {
		for _, dep := range m.Deps {
			ref := deps.Ref{Manifest: m.Path, Name: dep.Name}
			referred := slices.ContainsFunc(m.Constants(dep.Name), func(c string) bool { return consts[c] })
			if len(uses[ref]) == 0 && !referred {
				open[m.Path] = append(open[m.Path], dep.Name)
			}
			scopes := map[string][]string{} // package -> the scopes whose imports of it use the dependency
			for _, u := range uses[ref] {
				if !slices.Contains(scopes[u.key], u.scope) {
					scopes[u.key] = append(scopes[u.key], u.scope)
				}
			}
			for key, list := range scopes {
				slices.Sort(list)
				want.edge(edgeID{"PROVIDES", nodeID{"Dependency", m.Path + "|" + dep.Name}, nodeID{"Package", key}}, map[string]any{"scopes": list})
			}
		}
	}
	var scanned map[string]bool // the files of the graph, read when a fact needs them
	var scanErr error
	facts, err := deps.Facts(ctx, d.root, d.manifests, open, deps.Lookup{
		Imported: func(m deps.Manifest, name string) bool {
			for _, row := range rows {
				if str(row["key"]) == "ext:"+name && slices.Contains(m.Languages(), str(row["lang"])) && m.Governs(str(row["scope"])) {
					return true
				}
			}
			return false
		},
		Scanned: func(rel string) bool {
			if scanned == nil && scanErr == nil {
				scanned, scanErr = r.filePaths(ctx)
			}
			return scanned[rel]
		},
	})
	if err == nil {
		err = scanErr
	}
	if err != nil {
		return fmt.Errorf("dependencies: %w", err)
	}
	for _, m := range d.manifests {
		for _, dep := range m.Deps {
			props := map[string]any{"manifest": m.Path, "dir": m.Dir, "eco": m.Ecosystem, "name": dep.Name,
				"section": dep.Section, "line": dep.Line, "languages": m.Languages()}
			if cs := m.Constants(dep.Name); len(cs) > 0 {
				props["constants"] = cs
			}
			if f, ok := facts[m.Path][dep.Name]; ok {
				props["checked"] = true
				if f.Use != "" {
					props["use"] = f.Use
				}
				if f.Doubt != "" {
					props["doubt"] = f.Doubt
				}
			}
			want.node(nodeID{"Dependency", m.Path + "|" + dep.Name}, props)
		}
	}
	return nil
}

// rubyConstants are the top-level constants the project's Ruby files refer
// to that the repository does not declare (File.extConstants).
func (r *Repository) rubyConstants(ctx context.Context) (map[string]bool, error) {
	return r.stringSet(ctx, `
		MATCH (f:File {project: $project, language: 'ruby'})
		UNWIND f.extConstants AS c
		RETURN DISTINCT c AS value
	`)
}

// filePaths are the paths of the project's files.
func (r *Repository) filePaths(ctx context.Context) (map[string]bool, error) {
	return r.stringSet(ctx, `MATCH (f:File {project: $project}) RETURN f.path AS value`)
}

// stringSet is the set of the values a query returns.
func (r *Repository) stringSet(ctx context.Context, cypher string) (map[string]bool, error) {
	rows, err := r.query(ctx, cypher, nil, nil)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(rows))
	for _, row := range rows {
		out[str(row["value"])] = true
	}
	return out, nil
}

// uncheckedDoubt is the doubt of a dependency whose facts were not read
// (linking took it for imported): never reported as certain.
const uncheckedDoubt = "what else may use it was not read"

// unusedDependencies reports the dependencies a manifest declares that no
// file imports (a file in a language of the manifest and of a scope whose
// imports use the declaration; for a gem also a Ruby file that refers to one
// of its constants) and that nothing else uses (use). Not those
// in an ignored directory or named by the rule's ignore (by name, also
// for one ecosystem or one manifest only). doubt, when
// linking found one, makes the finding uncertain.
func (r *Repository) unusedDependencies(ctx context.Context, cfg rules.Config, ignore []string) ([]rules.Finding, error) {
	ignores, err := cfg.DependencyIgnores()
	if err != nil {
		return nil, err
	}
	ignored := make([]map[string]any, len(ignores))
	for i, ig := range ignores {
		// "" and no ecosystem: the entry applies to every manifest / ecosystem
		ignored[i] = map[string]any{"ecosystems": append([]string{}, ig.Ecosystems...), "manifest": ig.Manifest, "name": ig.Name}
	}
	rows, err := r.query(ctx, `
		MATCH (d:Dependency {project: $project})
		WHERE d.use IS NULL
		  AND NOT any(ig IN $ignored WHERE d.name =~ ig.name
		        AND (size(ig.ecosystems) = 0 OR d.eco IN ig.ecosystems)
		        AND (ig.manifest = '' OR d.manifest =~ ig.manifest))
		  AND NOT any(rx IN $ignore WHERE d.dir =~ rx)
		  AND NOT EXISTS {
		    MATCH (d)-[p:PROVIDES]->(:Package)<-[:IMPORTS]-(f:File {project: $project})
		    WHERE f.language IN d.languages AND coalesce(f.scope, '.') IN p.scopes
		  }
		  AND NOT EXISTS {
		    MATCH (f:File {project: $project, language: 'ruby'})
		    WHERE any(c IN d.constants WHERE c IN f.extConstants)
		  }
		RETURN d.manifest AS manifest, d.line AS line, d.name AS name,
		       CASE WHEN coalesce(d.checked, false) THEN d.doubt ELSE coalesce(d.doubt, $unchecked) END AS doubt
		ORDER BY manifest, name
	`, ignore, map[string]any{"ignored": ignored, "unchecked": uncheckedDoubt})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rules.RuleUnusedDependency, err)
	}
	out := make([]rules.Finding, 0, len(rows))
	for _, row := range rows {
		out = append(out, rules.UnusedDependency(cfg, str(row["manifest"]), toInt(row["line"]), str(row["name"]), strings.TrimSpace(str(row["doubt"]))))
	}
	return out, nil
}
