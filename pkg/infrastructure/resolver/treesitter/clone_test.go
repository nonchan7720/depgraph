package treesitter

import (
	"context"
	"testing"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

func symbolsByName(t *testing.T, root, rel string) map[string]shapeOf {
	t.Helper()
	r := New()
	t.Cleanup(r.Close)
	fg, err := r.ResolveFile(context.Background(), root, rel)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]shapeOf{}
	for _, s := range fg.Symbols {
		out[s.Name] = shapeOf{s.Tokens, s.CloneHash, s.Fingerprints}
	}
	return out
}

type shapeOf struct {
	tokens int
	hash   string
	fps    []int64
}

func jaccard(a, b []int64) float64 {
	sa, sb := map[int64]bool{}, map[int64]bool{}
	for _, x := range a {
		sa[x] = true
	}
	for _, x := range b {
		sb[x] = true
	}
	n := 0
	for x := range sa {
		if sb[x] {
			n++
		}
	}
	return float64(n) / float64(len(sa)+len(sb)-n)
}

// A function and its copy with every identifier and literal renamed (and a
// comment added) have the same shape; a copy with one more statement is
// still very similar; different logic is not.
func TestCodeShapeGo(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module ex.com/m\n")
	writeFile(t, root, "p/p.go", `package p

func Total(items []int, limit int) int {
	sum := 0
	for i, v := range items {
		if i >= limit {
			break
		}
		if v > 10 {
			sum += v * 2
		} else {
			sum += v
		}
	}
	return sum
}

// Sum is Total under other names.
func Sum(xs []int, max int) int {
	acc := 1 // a different literal
	for k, x := range xs {
		if k >= max {
			break
		}
		if x > 99 {
			acc += x * 3
		} else {
			acc += x
		}
	}
	return acc
}

func SumLogged(xs []int, max int) int {
	acc := 0
	for k, x := range xs {
		if k >= max {
			break
		}
		if x > 10 {
			acc += x * 2
		} else {
			acc += x
		}
	}
	println(acc)
	return acc
}

func Other(s string) []string {
	var out []string
	for _, r := range s {
		switch {
		case r == ' ':
			out = append(out, "")
		default:
			out[len(out)-1] += string(r)
		}
	}
	return out
}
`)
	s := symbolsByName(t, root, "p/p.go")
	if s["Total"].tokens < 40 || s["Total"].hash == "" || len(s["Total"].fps) == 0 {
		t.Fatalf("Total shape = %+v", s["Total"])
	}
	if s["Total"].hash != s["Sum"].hash {
		t.Error("a renamed copy must have the same hash")
	}
	if s["Total"].hash == s["SumLogged"].hash {
		t.Error("a changed copy must not have the same hash")
	}
	t.Logf("Total~SumLogged %.2f, Total~Other %.2f", jaccard(s["Total"].fps, s["SumLogged"].fps), jaccard(s["Total"].fps, s["Other"].fps))
	// a copy with one more statement is reported with the default options
	if j := jaccard(s["Total"].fps, s["SumLogged"].fps); j < rules.DefaultCloneOptions().MinSimilarity {
		t.Errorf("changed copy similarity = %.2f, want >= %.2f", j, rules.DefaultCloneOptions().MinSimilarity)
	}
	if j := jaccard(s["Total"].fps, s["Other"].fps); j > 0.2 {
		t.Errorf("different logic similarity = %.2f, want low", j)
	}
}

// TypeScript and Python functions get a shape too; types are not symbols
// whose code is compared.
func TestCodeShapeTSAndPython(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.ts", `export function a(x: number[]): number { let s = 0; for (const v of x) { if (v > 1) { s += v } } return s }
export const b = (y: number[]): number => { let t = 0; for (const w of y) { if (w > 2) { t += w } } return t }
export interface Shape { x: number }
`)
	writeFile(t, root, "m.py", `def a(xs):
    s = 0
    for v in xs:
        if v > 1:
            s += v
    return s

def b(ys):
    t = 0
    for w in ys:
        if w > 2:
            t += w
    return t
`)
	ts := symbolsByName(t, root, "a.ts")
	if ts["a"].hash == "" || ts["a"].hash != ts["b"].hash {
		t.Errorf("ts: a %+v, b %+v", ts["a"], ts["b"])
	}
	if ts["Shape"].hash != "" {
		t.Errorf("an interface has no code shape: %+v", ts["Shape"])
	}
	py := symbolsByName(t, root, "m.py")
	if py["a"].hash == "" || py["a"].hash != py["b"].hash {
		t.Errorf("py: a %+v, b %+v", py["a"], py["b"])
	}
}
