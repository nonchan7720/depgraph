package rules

import (
	"fmt"
	"strings"
	"testing"
	"time"

	graphrepo "github.com/nonchan7720/depgraph/pkg/infrastructure/repository/graph"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// The cases the Go implementation of low-cohesion was tested with, before
// the rule became Cypher over the linked parts (cohesion.go).

// lowCohesionOf stores the parts of units and returns the findings.
func lowCohesionOf(t *testing.T, units []rules.CohesionUnit) []rules.Finding {
	t.Helper()
	ctx, st := connect(t)
	key := fmt.Sprintf("zz_test_%d_cohesion", time.Now().UnixNano())
	t.Cleanup(func() { _ = graphrepo.ForProject(st, key).Clear(ctx) })
	r := New(st, key)
	want := newLinked()
	linkUnits(units, want)
	if err := r.storeLinked(ctx, want); err != nil {
		t.Fatal(err)
	}
	fs, err := r.lowCohesion(ctx, rules.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

func TestLowCohesion(t *testing.T) {
	split := rules.CohesionUnit{
		Key: "pkg/util", What: "package",
		Parts: []rules.CohesionPart{
			{ID: "parse.go", Names: []string{"Parse", "Token"}},
			{ID: "lex.go", Names: []string{"Lex"}},
			{ID: "render.go", Names: []string{"Render"}},
			{ID: "doc.go"},
		},
		Links: [][2]int{{0, 1}}, // parse.go uses lex.go
		Users: map[string][]string{
			"Parse":  {"pkg/config", "pkg/cli"},
			"Lex":    {"pkg/cli"},
			"Render": {"pkg/ui"},
		},
	}
	// the same, but one user takes both parts: they belong together for it
	shared := split
	shared.Key = "pkg/shared"
	shared.Users = map[string][]string{"Parse": {"pkg/config"}, "Render": {"pkg/config", "pkg/ui"}, "Lex": {"pkg/cli"}}
	// connected inside: nothing to split
	connected := split
	connected.Key = "pkg/connected"
	connected.Links = [][2]int{{0, 1}, {1, 2}}
	// a part nobody outside uses does not count
	small := rules.CohesionUnit{Key: "pkg/small", What: "package",
		Parts: []rules.CohesionPart{{ID: "a.go", Names: []string{"A"}}, {ID: "b.go", Names: []string{"B"}}},
		Users: map[string][]string{"A": {"x"}, "B": {"y"}}}
	got := lowCohesionOf(t, []rules.CohesionUnit{split, shared, connected, small})
	if len(got) != 1 || got[0].ID != "pkg/util" || got[0].Rule != rules.RuleLowCohesion {
		t.Fatalf("findings = %+v", got)
	}
	for _, want := range []string{
		"package pkg/util has 2 parts nothing inside connects, used by different code",
		"lex.go, parse.go (Lex, Parse) used by pkg/cli, pkg/config",
		"render.go (Render) used by pkg/ui",
		"split it along them",
	} {
		if !strings.Contains(got[0].Message, want) {
			t.Errorf("message %q lacks %q", got[0].Message, want)
		}
	}
}

// A package of data (entities, DTOs) used one type per consumer is normal
// architecture, not two responsibilities.
func TestLowCohesionSkipsData(t *testing.T) {
	models := rules.CohesionUnit{Key: "app/model", What: "package",
		Parts: []rules.CohesionPart{{ID: "user.go", Names: []string{"User", "NewUser"}, Data: true}, {ID: "order.go", Names: []string{"Order"}, Data: true}},
		Users: map[string][]string{"User": {"svc/user"}, "NewUser": {"svc/user"}, "Order": {"svc/order"}}}
	if got := lowCohesionOf(t, []rules.CohesionUnit{models}); len(got) != 0 {
		t.Errorf("data: %+v", got)
	}
	// behaviour on one side is enough to say the package does two things
	models.Parts[1].Data = false
	if got := lowCohesionOf(t, []rules.CohesionUnit{models}); len(got) != 1 {
		t.Errorf("data and behaviour: %+v", got)
	}
	if r, _ := rules.Lookup(rules.RuleLowCohesion); r.Severity != rules.SeverityInfo {
		t.Errorf("severity %s", r.Severity)
	}
}
