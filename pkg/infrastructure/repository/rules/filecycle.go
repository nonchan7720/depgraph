package rules

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// fileCycles finds the cycles between the files of one directory from
// what each file loads (graph.Usage.Loads). A cycle through several
// directories is a cycle of packages, which import-cycle reports, so only
// loads inside a directory are edges here.
func (r *Repository) fileCycles(ctx context.Context, cfg rules.Config, ignore []string) ([]rules.Finding, error) {
	rows, err := r.query(ctx, `
		MATCH (p:Package {project: $project})-[:CONTAINS]->(f:File)
		WHERE p.external = false AND none(rx IN $ignore WHERE p.key =~ rx)
		RETURN f.path AS file, f.loads AS loads
	`, ignore, nil)
	if err != nil {
		return nil, fmt.Errorf("file cycles: %w", err)
	}
	files := make(map[string]bool, len(rows))
	for _, row := range rows {
		files[fmt.Sprint(row["file"])] = true
	}
	var edges [][2]string
	for _, row := range rows {
		from := fmt.Sprint(row["file"])
		for _, s := range toStrings(row["loads"]) {
			u, ok := graph.DecodeUse(s)
			if !ok {
				continue
			}
			if to := loadedFile(files, u); to != "" && path.Dir(to) == path.Dir(from) {
				edges = append(edges, [2]string{from, to})
			}
		}
	}
	var out []rules.Finding
	for _, cyc := range rules.FileCycles(edges) {
		f := rules.FileCycle(cyc)
		f.Severity = string(cfg.Severity(rules.RuleFileCycle))
		out = append(out, f)
	}
	return out, nil
}

// loadedFile is the file of the project (files) a load names: the file
// itself, or for a Python package directory the submodule the imported name
// is, else the __init__.py that declares the name. "" when it is none.
func loadedFile(files map[string]bool, u graph.Use) string {
	if files[u.Target] {
		return u.Target
	}
	if first, _, _ := strings.Cut(u.Name, "."); first != "" {
		for _, mod := range []string{first + ".py", first + ".pyi", first + "/__init__.py"} {
			if p := path.Join(u.Target, mod); files[p] {
				return p
			}
		}
	}
	if p := path.Join(u.Target, "__init__.py"); files[p] {
		return p
	}
	return ""
}
