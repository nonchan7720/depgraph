package rules

import (
	"context"
	"errors"
	"fmt"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver/imports"
)

// rubyImports classifies the external requires and constants of Ruby files
// when the rules are evaluated, from the Gemfile.lock of the file's bundle
// (imports.GemLock): a gem the Gemfile asks for is third-party; anything
// else is unknown, and listed as unclassified only when it is a require, a
// gem locked as another gem's dependency or a constant that probably comes
// from a locked gem (Ruby's own constants are
// not listed: telling them apart would take a list of Ruby's per version).
func (r *Repository) rubyImports(ctx context.Context, ignore []string) (*pythonImports, error) {
	rows, err := r.query(ctx, `
		MATCH (a:Package {project: $project})-[:CONTAINS]->(f:File {language: 'ruby'})
		WHERE a.external = false AND NOT any(rx IN $ignore WHERE a.key =~ rx)
		OPTIONAL MATCH (f)-[:IMPORTS]->(b:Package {external: true})
		WHERE NOT any(rx IN $ignore WHERE b.key =~ rx)
		RETURN a.key AS pkg, f.path AS file, collect(DISTINCT b.key) AS requires, f.extConstants AS consts
	`, ignore, nil)
	if err != nil {
		return nil, fmt.Errorf("ruby imports: %w", err)
	}
	var locks *imports.GemLocks
	if r.root != "" {
		locks = imports.NewGemLocks(r.root)
	}
	out := &pythonImports{}
	for _, row := range rows {
		pkg, file := fmt.Sprint(row["pkg"]), fmt.Sprint(row["file"])
		var lock *imports.GemLock
		lockErr := errors.New("the project's files are not available here")
		if locks != nil {
			lock, lockErr = locks.For(file)
		}
		add := func(key string, required bool) {
			im := pythonImport{pkg: pkg, file: file, key: key, lang: graph.Ruby}
			if lock == nil {
				im.basis, im.unlisted = lockErr.Error(), !required
			} else if im.class, im.basis = lock.Classify(key); im.class == "" {
				if im.basis = lock.Unclassified(key); im.basis == "" {
					im.basis, im.unlisted = "not a gem the Gemfile asks for", !required
				}
			}
			out.list = append(out.list, im)
		}
		for _, k := range toStrings(row["requires"]) {
			add(k, true)
		}
		for _, c := range toStrings(row["consts"]) {
			add(imports.ExternalPrefix+c, false)
		}
	}
	return out, nil
}
