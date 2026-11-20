package rules

import (
	"context"
	"fmt"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// duplicate-code: the same (or nearly the same) logic in several places,
// whatever the names. Every function / method / class is stored with the
// shape of its body (Symbol.cloneHash: the token sequence with identifiers
// and literals masked; Symbol.fingerprints: winnowed hashes of its token
// k-grams). Linking connects the copies:
//
//	(Symbol {cloneGroup})-[:SIMILAR {score}]->(Symbol)
//
// between bodies with the same hash (no score), and between bodies that
// share a fingerprint that is not boilerplate and whose fingerprints are at
// least `similarity` the same (Jaccard, over all of them: the score). The
// rule reports the groups SIMILAR connects (Symbol.cloneGroup).

// cloneCandidates matches the bodies duplicate-code compares: at least
// $min tokens, not test code (table tests look alike on purpose), not in
// an ignored package.
const cloneCandidates = `
	MATCH (p:Package {project: $project})-[:CONTAINS]->(f:File)-[:DECLARES]->(s:Symbol)
	WHERE s.tokens >= $min AND none(rx IN $ignore WHERE p.key =~ rx) AND NOT f.path =~ $test
`

// cloneBucketSize is how many fingerprints one statement compares. The
// fingerprints are compared in as many statements as that takes, each with
// the fingerprints of one residue class: it bounds the rows (and the
// transaction memory) of a statement on a large repository, and a small one
// is compared in a single pass.
const cloneBucketSize = 500_000

// linkClones writes the SIMILAR relationships of the project for opt and
// removes those of an earlier link (gen tells them apart).
func (r *Repository) linkClones(ctx context.Context, opt rules.CloneOptions, ignore []string, gen string) error {
	p := map[string]any{
		"project": r.project, "ignore": ignore, "test": rules.TestFileRegex, "gen": gen,
		"min": max(opt.MinTokens, 1), "similarity": opt.MinSimilarity, "maxFreq": opt.MaxFingerprintFreq,
	}
	rows, err := r.query(ctx, cloneCandidates+`RETURN sum(size(s.fingerprints)) AS fingerprints`, ignore, p)
	if err != nil {
		return fmt.Errorf("duplicate code: %w", err)
	}
	buckets := 1
	if len(rows) == 1 {
		buckets = max(1, (toInt(rows[0]["fingerprints"])+cloneBucketSize-1)/cloneBucketSize)
	}
	p["buckets"] = buckets
	// identical after masking names and literals: each copy to the first
	if err := r.write(ctx, cloneCandidates+`
		  AND s.cloneHash IS NOT NULL
		WITH s.cloneHash AS hash, s ORDER BY f.path, s.line, s.name
		WITH hash, collect(s) AS copies
		WHERE size(copies) > 1
		WITH copies[0] AS first, copies[1..] AS others
		UNWIND others AS other
		MERGE (first)-[e:SIMILAR]->(other)
		SET e.gen = $gen, e.score = null
	`, p); err != nil {
		return fmt.Errorf("duplicate code: %w", err)
	}
	// similar: bodies that share a fingerprint found in at most maxFreq
	// bodies (one found in more is boilerplate: error handling, loops) are
	// compared over all their fingerprints. Comparing only the rare ones
	// made two unrelated, mostly-boilerplate functions that share a single
	// rare fingerprint look alike.
	for bucket := range buckets {
		p["bucket"] = bucket
		if err := r.write(ctx, cloneCandidates+`
			UNWIND s.fingerprints AS fp
			WITH s, fp WHERE abs(fp) % $buckets = $bucket
			WITH fp, collect(DISTINCT s) AS bodies
			WHERE size(bodies) > 1 AND size(bodies) <= $maxFreq
			UNWIND range(0, size(bodies) - 2) AS i
			UNWIND range(i + 1, size(bodies) - 1) AS j
			WITH bodies[i] AS x, bodies[j] AS y
			WITH DISTINCT CASE WHEN elementId(x) < elementId(y) THEN x ELSE y END AS a,
			              CASE WHEN elementId(x) < elementId(y) THEN y ELSE x END AS b
			WITH a, b, size([v IN a.fingerprints WHERE v IN b.fingerprints]) AS shared
			WITH a, b, toFloat(shared) / (size(a.fingerprints) + size(b.fingerprints) - shared) AS score
			WHERE score >= $similarity
			MERGE (a)-[e:SIMILAR]-(b)
			SET e.gen = $gen, e.score = score
		`, p); err != nil {
			return fmt.Errorf("duplicate code: %w", err)
		}
	}
	if err := r.write(ctx, `
		MATCH (:Symbol {project: $project})-[e:SIMILAR]->(:Symbol)
		WHERE e.gen <> $gen
		DELETE e
	`, p); err != nil {
		return fmt.Errorf("duplicate code: %w", err)
	}
	// the group of a copy: the bodies SIMILAR connects to it, directly or
	// through other copies (A~B and B~C are one group), named after one of them
	if err := r.write(ctx, `
		MATCH (s:Symbol {project: $project})
		WHERE s.cloneGroup IS NOT NULL AND NOT (s)-[:SIMILAR]-()
		REMOVE s.cloneGroup
	`, p); err != nil {
		return fmt.Errorf("duplicate code: %w", err)
	}
	if err := r.write(ctx, `
		MATCH (s:Symbol {project: $project})
		WHERE (s)-[:SIMILAR]-()
		MATCH (s)-[:SIMILAR*0..]-(t:Symbol)
		WITH DISTINCT s, t
		WITH s, min(elementId(t)) AS grp
		SET s.cloneGroup = grp
	`, p); err != nil {
		return fmt.Errorf("duplicate code: %w", err)
	}
	return nil
}

// duplicateCode reports the groups of copies (Symbol.cloneGroup).
func (r *Repository) duplicateCode(ctx context.Context, cfg rules.Config) ([]rules.Finding, error) {
	rows, err := r.query(ctx, `
		MATCH (f:File {project: $project})-[:DECLARES]->(s:Symbol)
		WHERE s.cloneGroup IS NOT NULL
		WITH s, f, s.cloneGroup AS grp
		OPTIONAL MATCH (s)-[e:SIMILAR]->(:Symbol)
		WITH grp, s, f, min(e.score) AS low
		ORDER BY f.path + ':' + s.name, s.line
		WITH grp, min(low) AS low,
		     collect({file: f.path, name: s.name, kind: s.kind, line: s.line, endLine: s.endLine, hash: s.cloneHash}) AS members
		RETURN members, coalesce(low, 1.0) AS low,
		       all(m IN members WHERE m.hash IS NOT NULL AND m.hash = members[0].hash) AS identical
	`, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("duplicate code: %w", err)
	}
	out := make([]rules.Finding, 0, len(rows))
	for _, row := range rows {
		identical, _ := row["identical"].(bool)
		low, _ := row["low"].(float64)
		f := rules.DuplicateCode(cloneMembers(row["members"]), identical, low)
		f.Severity = string(cfg.Severity(rules.RuleDuplicateCode))
		out = append(out, f)
	}
	return out, nil
}

// cloneMembers are the copies of a row of the query above.
func cloneMembers(v any) []rules.CloneMember {
	list, _ := v.([]any)
	members := make([]rules.CloneMember, 0, len(list))
	for _, x := range list {
		m, _ := x.(map[string]any)
		members = append(members, rules.CloneMember{File: str(m["file"]), Name: str(m["name"]), Kind: str(m["kind"]),
			Line: toInt(m["line"]), EndLine: toInt(m["endLine"])})
	}
	return members
}
