package rules

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// legacyCycles is the import-cycle query before it was replaced: every
// elementary cycle of 2..8 packages. It is exponential in the number of
// cycles and kept here only as the reference for the new one.
const legacyCycles = `
	MATCH p = (a:Package {project: $project})-[:IMPORTS*2..8]->(a)
	WHERE a.external = false
	WITH [n IN nodes(p) | n.key] AS cyc
	WITH cyc, reduce(m = cyc[0], x IN cyc | CASE WHEN x < m THEN x ELSE m END) AS minKey
	WHERE cyc[0] = minKey
	RETURN DISTINCT cyc`

func cycleEdges(cycles [][]string) []string {
	set := map[string]bool{}
	for _, c := range cycles {
		for i := 0; i+1 < len(c); i++ {
			set[c[i]+">"+c[i+1]] = true
		}
	}
	out := make([]string, 0, len(set))
	for e := range set {
		out = append(out, e)
	}
	sort.Strings(out)
	return out
}

// The import-cycle rule reports, for every import that lies on a cycle of at
// most 8 packages, a cycle through it: on random graphs the imports covered
// are exactly those the exhaustive query covers, and every reported cycle is
// a real one.
func TestCyclesCoverTheSameImportsAsTheExhaustiveQuery(t *testing.T) {
	ctx, st := connect(t)
	rng := rand.New(rand.NewPCG(1, 2))
	for g := range 25 {
		key := fmt.Sprintf("zz_test_%d_cyc%d", time.Now().UnixNano(), g)
		n := 3 + rng.IntN(6)
		pkgs := map[string][]string{}
		edges := map[string]bool{}
		for i := range n {
			from := fmt.Sprintf("p%d", i)
			pkgs[from] = nil
			for j := range n {
				if i != j && rng.Float64() < 0.3 {
					to := fmt.Sprintf("p%d", j)
					pkgs[from] = append(pkgs[from], to)
					edges[from+">"+to] = true
				}
			}
		}
		seed(ctx, t, st, key, pkgs)

		rows, err := New(st, key).query(ctx, legacyCycles, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		var want [][]string
		for _, r := range rows {
			want = append(want, toStrings(r["cyc"]))
		}
		fs, err := New(st, key).Evaluate(ctx, rules.DefaultConfig())
		if err != nil {
			t.Fatal(err)
		}
		var got [][]string
		for _, f := range fs {
			if f.Rule != rules.RuleImportCycle {
				continue
			}
			c, _ := f.Data["packages"].([]string)
			got = append(got, c)
			if len(c) < 3 || c[0] != c[len(c)-1] || len(c) > 9 {
				t.Errorf("graph %d: not a cycle: %v", g, c)
			}
			for i := 0; i+1 < len(c); i++ {
				if !edges[c[i]+">"+c[i+1]] {
					t.Errorf("graph %d: cycle %v uses a missing import %s>%s", g, c, c[i], c[i+1])
				}
			}
		}
		if w, gt := strings.Join(cycleEdges(want), " "), strings.Join(cycleEdges(got), " "); w != gt {
			t.Errorf("graph %d (%v): imports on cycles\n got  %s\n want %s", g, pkgs, gt, w)
		}
	}
}

// A dense graph has an exponential number of elementary cycles; the rule
// must stay well inside the hook's time budget.
func TestCyclesOnADenseGraphAreFast(t *testing.T) {
	ctx, st := connect(t)
	key := fmt.Sprintf("zz_test_%d_dense", time.Now().UnixNano())
	pkgs := map[string][]string{}
	const n = 40
	for i := range n {
		from := fmt.Sprintf("p%02d", i)
		for d := 1; d <= 6; d++ {
			pkgs[from] = append(pkgs[from], fmt.Sprintf("p%02d", (i+d)%n))
		}
		pkgs[from] = append(pkgs[from], fmt.Sprintf("p%02d", (i+n-1)%n))
	}
	seed(ctx, t, st, key, pkgs)
	start := time.Now()
	fs, err := New(st, key).Evaluate(ctx, rules.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("Evaluate took %s on a dense graph", d)
	}
	cycles := 0
	for _, f := range fs {
		if f.Rule == rules.RuleImportCycle {
			cycles++
		}
	}
	if cycles == 0 || cycles > 50 {
		t.Errorf("%d cycles reported, want 1..50", cycles)
	}

	// an ignored package in the middle makes shortestPath look for paths
	// around it: still fast, and no cycle goes through it
	cfg := rules.DefaultConfig()
	cfg.Ignore = []string{"p00", "p13"}
	start = time.Now()
	fs, err = New(st, key).Evaluate(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("Evaluate with ignored packages took %s", d)
	}
	for _, f := range fs {
		if f.Rule != rules.RuleImportCycle {
			continue
		}
		for _, k := range f.Data["packages"].([]string) {
			if k == "p00" || k == "p13" {
				t.Errorf("cycle through an ignored package: %v", f.Data["packages"])
			}
		}
	}
}
