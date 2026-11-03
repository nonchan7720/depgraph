package rules

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// CloneCandidate is one function / method / class with the shape of its code:
// Hash identifies its normalised token sequence (identifiers and literals
// masked, so a renamed copy has the same hash) and Fingerprints are the
// winnowed hashes of its token k-grams (shared by a copy that was changed a
// little).
type CloneCandidate struct {
	File, Name, Kind string
	Line, EndLine    int
	Tokens           int
	Hash             string
	Fingerprints     []int64
}

func (c CloneCandidate) ref() string { return c.File + ":" + c.Name }

// CloneOptions tune duplicate-code; a zero field is the default.
type CloneOptions struct {
	// MinTokens: smaller code is not worth reporting (getters, one-liners).
	MinTokens int
	// MinSimilarity: Jaccard similarity of fingerprints for a near copy.
	MinSimilarity float64
	// MaxFingerprintFreq: a fingerprint found in more candidates than this
	// is boilerplate (error handling, loops), not evidence of copying.
	MaxFingerprintFreq int
}

// withDefaults fills unset (zero) options with the defaults.
func (o CloneOptions) withDefaults() CloneOptions {
	d := DefaultCloneOptions()
	if o.MinTokens == 0 {
		o.MinTokens = d.MinTokens
	}
	if o.MinSimilarity == 0 {
		o.MinSimilarity = d.MinSimilarity
	}
	if o.MaxFingerprintFreq == 0 {
		o.MaxFingerprintFreq = d.MaxFingerprintFreq
	}
	return o
}

// DefaultCloneOptions returns the defaults of duplicate-code.
func DefaultCloneOptions() CloneOptions {
	return CloneOptions{MinTokens: 50, MinSimilarity: 0.8, MaxFingerprintFreq: 10}
}

// FindClones groups candidates whose code is identical (same Hash) or
// similar (fingerprint Jaccard similarity >= MinSimilarity). Chains are
// merged: A~B and B~C are one group. One finding per group, sorted.
func FindClones(cands []CloneCandidate, opt CloneOptions) []Finding {
	opt = opt.withDefaults()
	var cs []CloneCandidate
	for _, c := range cands {
		if c.Tokens >= opt.MinTokens {
			cs = append(cs, c)
		}
	}
	parent := make([]int, len(cs))
	for i := range parent {
		parent[i] = i
	}
	find := func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	union := func(a, b int) { parent[find(a)] = find(b) }

	// identical (after masking names and literals)
	byHash := map[string]int{}
	for i, c := range cs {
		if c.Hash == "" {
			continue
		}
		if j, ok := byHash[c.Hash]; ok {
			union(i, j)
		} else {
			byHash[c.Hash] = i
		}
	}

	// similar: candidate pairs share a fingerprint that is not boilerplate
	sets := make([]map[int64]bool, len(cs))
	index := map[int64][]int{}
	for i, c := range cs {
		sets[i] = map[int64]bool{}
		for _, f := range c.Fingerprints {
			if !sets[i][f] {
				sets[i][f] = true
				index[f] = append(index[f], i)
			}
		}
	}
	common := func(f int64) bool { return len(index[f]) > opt.MaxFingerprintFreq }
	// Candidates share a fingerprint that is not boilerplate; their
	// similarity is then measured over ALL fingerprints. Measuring it over
	// the non-boilerplate remainder made two unrelated, mostly-boilerplate
	// functions sharing a single rare fingerprint look 100% alike.
	cand := map[[2]int]bool{}
	for f, members := range index {
		if common(f) {
			continue
		}
		for a := range members {
			for b := a + 1; b < len(members); b++ {
				cand[[2]int{members[a], members[b]}] = true
			}
		}
	}
	sim := map[[2]int]float64{}
	for p := range cand {
		n := 0
		for f := range sets[p[0]] {
			if sets[p[1]][f] {
				n++
			}
		}
		j := float64(n) / float64(len(sets[p[0]])+len(sets[p[1]])-n)
		if j >= opt.MinSimilarity {
			union(p[0], p[1])
			sim[p] = j
		}
	}

	groups := map[int][]int{}
	for i := range cs {
		groups[find(i)] = append(groups[find(i)], i)
	}
	var out []Finding
	for _, g := range groups {
		if len(g) < 2 {
			continue
		}
		sort.Slice(g, func(a, b int) bool { return cs[g[a]].ref() < cs[g[b]].ref() })
		identical := true
		low := 1.0
		for _, i := range g[1:] {
			if cs[i].Hash == "" || cs[i].Hash != cs[g[0]].Hash {
				identical = false
			}
		}
		if !identical {
			for a := range g {
				for b := a + 1; b < len(g); b++ {
					p := [2]int{min(g[a], g[b]), max(g[a], g[b])}
					if s, ok := sim[p]; ok && s < low {
						low = s
					}
				}
			}
		}
		out = append(out, duplicateCode(cs, g, identical, low))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out
}

func duplicateCode(cs []CloneCandidate, g []int, identical bool, similarity float64) Finding {
	refs := make([]string, len(g))
	places := make([]string, len(g))
	members := make([]map[string]any, len(g))
	for k, i := range g {
		c := cs[i]
		refs[k] = c.ref()
		places[k] = fmt.Sprintf("%s:%d-%d %s", c.File, c.Line, c.EndLine, c.Name)
		members[k] = map[string]any{"file": c.File, "name": c.Name, "kind": c.Kind, "line": c.Line, "endLine": c.EndLine}
	}
	what := "identical logic"
	if !identical {
		// floor: never round 79.6% up to the 80% threshold
		what = fmt.Sprintf("similar logic (%d%%)", int(math.Floor(similarity*100)))
	}
	return Finding{
		Rule: "duplicate-code", Severity: "warn",
		ID:      strings.Join(refs, "|"),
		Message: fmt.Sprintf("%s in %d places: %s", what, len(g), strings.Join(places, ", ")),
		Data:    map[string]any{"members": members, "identical": identical},
	}
}

// IsTestFile reports whether a repo-relative path is test code by the
// conventions of the supported languages.
func IsTestFile(p string) bool {
	base := p[strings.LastIndex(p, "/")+1:]
	switch {
	case strings.HasSuffix(base, "_test.go"),
		strings.Contains(base, ".test."), strings.Contains(base, ".spec."),
		strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py"),
		strings.HasSuffix(base, "_test.py"), base == "conftest.py":
		return true
	}
	for _, dir := range []string{"__tests__/", "tests/", "test/", "testdata/"} {
		if strings.HasPrefix(p, dir) || strings.Contains(p, "/"+dir) {
			return true
		}
	}
	return false
}
