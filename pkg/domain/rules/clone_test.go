package rules

import (
	"fmt"
	"strings"
	"testing"
)

// fps returns the fingerprints lo..hi-1.
func fps(lo, hi int64) []int64 {
	var out []int64
	for i := lo; i < hi; i++ {
		out = append(out, i)
	}
	return out
}

func cand(file, name string, line, tokens int, hash string, fp []int64) CloneCandidate {
	return CloneCandidate{File: file, Name: name, Kind: "function", Line: line, EndLine: line + 9, Tokens: tokens, Hash: hash, Fingerprints: fp}
}

func TestFindClones(t *testing.T) {
	opt := CloneOptions{MinTokens: 50, MinSimilarity: 0.8, MaxFingerprintFreq: 3}
	got := FindClones([]CloneCandidate{
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
	got := FindClones([]CloneCandidate{
		cand("a.go", "A", 1, 80, "x", fps(0, 20)),
		cand("b.go", "B", 1, 80, "y", fps(1, 21)),
		cand("c.go", "C", 1, 80, "y", fps(1, 21)),
	}, CloneOptions{MinTokens: 50, MinSimilarity: 0.8, MaxFingerprintFreq: 10})
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
	if got := FindClones(cs, CloneOptions{MinTokens: 50, MinSimilarity: 0.8, MaxFingerprintFreq: 5}); len(got) != 0 {
		t.Errorf("boilerplate reported as duplicates: %v", got)
	}
}

func TestFindClonesDefaults(t *testing.T) {
	d := DefaultCloneOptions()
	if d.MinTokens <= 0 || d.MinSimilarity <= 0 || d.MinSimilarity > 1 || d.MaxFingerprintFreq <= 1 {
		t.Errorf("defaults = %+v", d)
	}
}

func TestIsTestFile(t *testing.T) {
	for p, want := range map[string]bool{
		"a/a_test.go": true, "web/src/x.test.ts": true, "src/x.spec.tsx": true, "app/test_x.py": true,
		"app/x_test.py": true, "tests/x.py": true, "src/__tests__/a.js": true, "pkg/x/testdata/a.go": true,
		"conftest.py": true,
		"a/a.go":      false, "src/latest.ts": false, "app/contest.py": false, "pkg/attest/a.go": false,
	} {
		if got := IsTestFile(p); got != want {
			t.Errorf("IsTestFile(%q) = %v, want %v", p, got, want)
		}
	}
}

// A zero CloneOptions (a Config built by hand) means the defaults.
func TestFindClonesZeroOptionsAreDefaults(t *testing.T) {
	cs := []CloneCandidate{cand("a.go", "A", 1, 80, "h", fps(0, 20)), cand("b.go", "B", 1, 80, "h", fps(0, 20)), cand("c.go", "C", 1, 10, "h", fps(0, 20))}
	if got := FindClones(cs, CloneOptions{}); len(got) != 1 || !strings.Contains(got[0].Message, "in 2 places") {
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
	for _, f := range FindClones(cs, CloneOptions{MinTokens: 50, MinSimilarity: 0.8, MaxFingerprintFreq: 10}) {
		if strings.Contains(f.Message, "a.go") || strings.Contains(f.Message, "b.go") {
			t.Errorf("unrelated functions reported: %s", f.Message)
		}
	}
}

// Copies with the same file and name (String of two types, Go's _ funcs)
// are listed by line, whatever order they come in: the message must not
// change between runs.
func TestFindClonesSameNameInAFileIsStable(t *testing.T) {
	opt := CloneOptions{MinTokens: 50, MinSimilarity: 0.8, MaxFingerprintFreq: 5}
	a := cand("m/file.go", "String", 541, 80, "h", fps(0, 20))
	b := cand("m/file.go", "String", 535, 80, "h", fps(0, 20))
	c := cand("m/file.go", "String", 23, 80, "h", fps(0, 20))
	want := "identical logic in 3 places: m/file.go:23-32 String, m/file.go:535-544 String, m/file.go:541-550 String"
	for _, order := range [][]CloneCandidate{{a, b, c}, {c, a, b}, {b, c, a}} {
		got := FindClones(order, opt)
		if len(got) != 1 || got[0].Message != want {
			t.Errorf("%v: %+v", order[0].Line, got)
		}
	}
}
