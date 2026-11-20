package rules

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// The cases the Go implementation of duplicate-code was tested with,
// before the rule became Cypher over SIMILAR (clones.go): the same bodies,
// the same findings.

// CloneCandidate is one function with the shape of its body.
type CloneCandidate struct {
	File, Name   string
	Line, Tokens int
	Hash         string
	Fingerprints []int64
}

// fps returns the fingerprints lo..hi-1.
func fps(lo, hi int64) []int64 {
	var out []int64
	for i := lo; i < hi; i++ {
		out = append(out, i)
	}
	return out
}

func cand(file, name string, line, tokens int, hash string, fp []int64) CloneCandidate {
	return CloneCandidate{File: file, Name: name, Line: line, Tokens: tokens, Hash: hash, Fingerprints: fp}
}

// FindClones stores the candidates (in the order given) and returns the
// findings of duplicate-code with opt.
func FindClones(t *testing.T, cands []CloneCandidate, opt rules.CloneOptions) []rules.Finding {
	t.Helper()
	ctx, st := connect(t)
	key := fmt.Sprintf("zz_test_%d_clones", time.Now().UnixNano())
	var files []*graph.FileGraph
	byPath := map[string]*graph.FileGraph{}
	for _, c := range cands {
		fg := byPath[c.File]
		if fg == nil {
			lang := graph.Go
			switch {
			case strings.HasSuffix(c.File, ".ts"):
				lang = graph.TypeScript
			case strings.HasSuffix(c.File, ".py"):
				lang = graph.Python
			}
			fg = &graph.FileGraph{Path: c.File, Package: graph.PackageKey(c.File), Language: lang}
			byPath[c.File] = fg
			files = append(files, fg)
		}
		fg.Symbols = append(fg.Symbols, graph.Symbol{Name: c.Name, Kind: "function", Line: c.Line, EndLine: c.Line + 9,
			Tokens: c.Tokens, CloneHash: c.Hash, Fingerprints: c.Fingerprints})
	}
	upsertAll(ctx, t, st, key, files)
	cfg := rules.DefaultConfig()
	cfg.Clones = opt
	fs, err := New(st, key).Evaluate(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return byRule(fs, rules.RuleDuplicateCode)
}

func TestFindClones(t *testing.T) {
	opt := rules.CloneOptions{MinTokens: 50, MinSimilarity: 0.8, MaxFingerprintFreq: 3}
	got := FindClones(t, []CloneCandidate{
		// the same logic under different names, in different files
		cand("b/b.go", "Sum", 10, 80, "h1", fps(0, 20)),
		cand("a/a.go", "Total", 3, 80, "h1", fps(0, 20)),
		// a copy with a few changes: 18 of 22 distinct fingerprints shared (Jaccard 0.818)
		cand("c/c.ts", "parseRow", 1, 90, "h2", fps(100, 120)),
		cand("d/d.ts", "readRow", 7, 92, "h3", fps(102, 122)),
		// unrelated
		cand("e/e.py", "other", 1, 90, "h4", fps(200, 220)),
		// half the same: below the threshold
		cand("f/f.py", "half", 1, 90, "h5", fps(200, 210)),
		// too small to matter, even if identical
		cand("g/g.go", "tiny", 1, 20, "h6", fps(300, 305)),
		cand("h/h.go", "tiny2", 1, 20, "h6", fps(300, 305)),
	}, opt)
	var msgs []string
	for _, f := range got {
		if f.Rule != "duplicate-code" || f.Severity != "warn" {
			t.Errorf("finding %+v", f)
		}
		msgs = append(msgs, f.Message)
	}
	want := []string{
		"identical logic in 2 places: a/a.go:3-12 Total, b/b.go:10-19 Sum",
		"similar logic (81%) in 2 places: c/c.ts:1-10 parseRow, d/d.ts:7-16 readRow",
	}
	if strings.Join(msgs, "\n") != strings.Join(want, "\n") {
		t.Errorf("messages:\n%s\nwant:\n%s", strings.Join(msgs, "\n"), strings.Join(want, "\n"))
	}
	// identity does not depend on line numbers (edits above a function move it)
	if got[0].ID != "a/a.go:Total|b/b.go:Sum" {
		t.Errorf("ID = %q", got[0].ID)
	}
}

// Copies chain into one group: A~B and B~C report A, B and C together.
func TestFindClonesGroupsChains(t *testing.T) {
	got := FindClones(t, []CloneCandidate{
		cand("a.go", "A", 1, 80, "x", fps(0, 20)),
		cand("b.go", "B", 1, 80, "y", fps(1, 21)),
		cand("c.go", "C", 1, 80, "y", fps(1, 21)),
	}, rules.CloneOptions{MinTokens: 50, MinSimilarity: 0.8, MaxFingerprintFreq: 10})
	if len(got) != 1 || !strings.Contains(got[0].Message, "in 3 places") {
		t.Fatalf("got %v", got)
	}
}

// Boilerplate shared by many functions (`if err != nil { return err }`) is
// not evidence of copying: fingerprints in too many functions are ignored.
func TestFindClonesIgnoresBoilerplate(t *testing.T) {
	var cs []CloneCandidate
	for i := range 6 {
		// everything shared is common boilerplate (fingerprints 0..15), the rest is unique
		cs = append(cs, cand(fmt.Sprintf("p%d.go", i), fmt.Sprintf("F%d", i), 1, 80, fmt.Sprint(i),
			append(fps(0, 16), fps(int64(100*(i+1)), int64(100*(i+1)+4))...)))
	}
	if got := FindClones(t, cs, rules.CloneOptions{MinTokens: 50, MinSimilarity: 0.8, MaxFingerprintFreq: 5}); len(got) != 0 {
		t.Errorf("boilerplate reported as duplicates: %v", got)
	}
}

// A zero CloneOptions (a Config built by hand) means the defaults.
func TestFindClonesZeroOptionsAreDefaults(t *testing.T) {
	cs := []CloneCandidate{cand("a.go", "A", 1, 80, "h", fps(0, 20)), cand("b.go", "B", 1, 80, "h", fps(0, 20)), cand("c.go", "C", 1, 10, "h", fps(0, 20))}
	if got := FindClones(t, cs, rules.CloneOptions{}); len(got) != 1 || !strings.Contains(got[0].Message, "in 2 places") {
		t.Errorf("zero options: %v", got)
	}
}

// Two unrelated functions made mostly of boilerplate share one rare
// fingerprint: after dropping the boilerplate they looked 100% alike. The
// similarity is measured over all fingerprints; boilerplate only stops a
// pair from becoming a candidate on its own.
func TestFindClonesSmallRemainderIsNotACopy(t *testing.T) {
	var cs []CloneCandidate
	// fillers make fingerprints 0..9 and 10..19 common
	for i := range 12 {
		cs = append(cs, cand(fmt.Sprintf("f%d.go", i), fmt.Sprintf("F%d", i), 1, 60, fmt.Sprint("f", i), append(fps(0, 10), fps(10, 20)...)))
	}
	cs = append(cs,
		cand("a.go", "A", 1, 68, "ha", append(fps(0, 10), 900)),  // boilerplate + 1 rare
		cand("b.go", "B", 1, 51, "hb", append(fps(10, 20), 900)), // other boilerplate + the same rare one
	)
	for _, f := range FindClones(t, cs, rules.CloneOptions{MinTokens: 50, MinSimilarity: 0.8, MaxFingerprintFreq: 10}) {
		if strings.Contains(f.Message, "a.go") || strings.Contains(f.Message, "b.go") {
			t.Errorf("unrelated functions reported: %s", f.Message)
		}
	}
}

// Copies with the same file and name (String of two types, Go's _ funcs)
// are listed by line, whatever order they come in: the message must not
// change between runs.
func TestFindClonesSameNameInAFileIsStable(t *testing.T) {
	opt := rules.CloneOptions{MinTokens: 50, MinSimilarity: 0.8, MaxFingerprintFreq: 5}
	a := cand("m/file.go", "String", 541, 80, "h", fps(0, 20))
	b := cand("m/file.go", "String", 535, 80, "h", fps(0, 20))
	c := cand("m/file.go", "String", 23, 80, "h", fps(0, 20))
	want := "identical logic in 3 places: m/file.go:23-32 String, m/file.go:535-544 String, m/file.go:541-550 String"
	for _, order := range [][]CloneCandidate{{a, b, c}, {c, a, b}, {b, c, a}} {
		got := FindClones(t, order, opt)
		if len(got) != 1 || got[0].Message != want {
			t.Errorf("%v: %+v", order[0].Line, got)
		}
	}
}
