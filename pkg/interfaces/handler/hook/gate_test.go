package hook

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// session-start records the session's baseline once: a resumed or compacted
// session keeps the findings it started with.
func TestRunSessionStartRecordsBaselineOnce(t *testing.T) {
	root := t.TempDir()
	src := &fakeProject{data: map[string][]rules.Finding{root: {f("god-package", "warn", "x")}}}
	ctx, now := context.Background(), time.Now()
	if _, err := Run(ctx, SessionStart, Input{SessionID: "s1"}, root, src, now); err != nil {
		t.Fatal(err)
	}
	src.data[root] = []rules.Finding{f("import-cycle", "error", "a -> b -> a")}
	if _, err := Run(ctx, SessionStart, Input{SessionID: "s1", Source: "compact"}, root, src, now); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, Prompt, Input{SessionID: "s1"}, root, src, now); err != nil {
		t.Fatal(err)
	}
	base, ok, _ := src.sessions.Baseline(ctx, "s1")
	if !ok || len(base) != 1 || base[0].Rule != "god-package" {
		t.Fatalf("baseline = %+v, %v; want the findings at the first session-start", base, ok)
	}
}

// fakeGraph applies changes and evaluates: each Apply moves to the next state.
type fakeGraph struct {
	states  [][]rules.Finding // findings before the first apply, after it, ...
	applied [][]string
	evals   int
	err     error
}

func (g *fakeGraph) deps(s SessionStore) GateDeps {
	return GateDeps{
		Sessions: s,
		Apply: func(_ context.Context, files []string) error {
			g.applied = append(g.applied, files)
			return g.err
		},
		Evaluate: func(context.Context) ([]rules.Finding, error) {
			g.evals++
			return g.states[min(len(g.applied), len(g.states)-1)], nil
		},
	}
}

func TestGateReportsOnlyDebtAddedInTheSession(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	s := &fakeSessions{}
	_ = s.PutBaseline(ctx, "s1", []rules.Finding{f("god-package", "warn", "pkg/a")}, now)
	g := &fakeGraph{states: [][]rules.Finding{
		{f("god-package", "warn", "pkg/a")},
		{f("god-package", "warn", "pkg/a"), f("import-cycle", "error", "a -> b -> a"), f("unused-export", "warn", "F in x.go"), f("x", "info", "note")},
	}}
	out, err := gateText(ctx, g.deps(s), "s1", []string{"x.go"}, rules.SeverityWarn, now)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(g.applied[0], []string{"x.go"}) {
		t.Errorf("applied %v", g.applied)
	}
	for _, want := range []string{"2 new finding", "[error] import-cycle: a -> b -> a", "[warn] unused-export: F in x.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	for _, not := range []string{"god-package", "note"} {
		if strings.Contains(out, not) {
			t.Errorf("report has %q (in the baseline / below --fail-on):\n%s", not, out)
		}
	}
	if strings.Index(out, "import-cycle") > strings.Index(out, "unused-export") {
		t.Errorf("errors must come first:\n%s", out)
	}
}

func TestGatePassesWhenNothingWasAdded(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	s := &fakeSessions{}
	_ = s.PutBaseline(ctx, "s1", []rules.Finding{f("god-package", "warn", "pkg/a")}, now)
	g := &fakeGraph{states: [][]rules.Finding{{f("god-package", "warn", "pkg/a")}, {}}} // the edit fixed debt
	if out, err := gateText(ctx, g.deps(s), "s1", []string{"x.go"}, rules.SeverityWarn, now); err != nil || out != "" {
		t.Fatalf("RunGate = %q, %v; want a pass", out, err)
	}
	// --fail-on error: a new warn does not fail.
	g = &fakeGraph{states: [][]rules.Finding{{}, {f("unused-export", "warn", "F")}}}
	if out, _ := gateText(ctx, g.deps(s), "s1", []string{"x.go"}, rules.SeverityError, now); out != "" {
		t.Fatalf("warn reported with --fail-on error: %q", out)
	}
}

// Without a session-start baseline (the hook is not installed, or no store
// ran then), the findings before this run's changes become the baseline.
func TestGateWithoutBaselineUsesTheStateBeforeTheChanges(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	s := &fakeSessions{}
	g := &fakeGraph{states: [][]rules.Finding{{f("god-package", "warn", "pkg/a")}, {f("god-package", "warn", "pkg/a"), f("import-cycle", "error", "c")}}}
	out, err := gateText(ctx, g.deps(s), "s1", []string{"x.go"}, rules.SeverityWarn, now)
	if err != nil || !strings.Contains(out, "import-cycle") || strings.Contains(out, "god-package") {
		t.Fatalf("RunGate = %q, %v", out, err)
	}
	if base, ok, _ := s.Baseline(ctx, "s1"); !ok || len(base) != 1 {
		t.Errorf("baseline not recorded before the changes: %+v", base)
	}
}

// The stop check runs without files: nothing is applied, one evaluation.
func TestGateWithoutFilesOnlyEvaluates(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	s := &fakeSessions{}
	_ = s.PutBaseline(ctx, "s1", nil, now)
	g := &fakeGraph{states: [][]rules.Finding{{f("import-cycle", "error", "c")}}}
	out, _ := gateText(ctx, g.deps(s), "s1", nil, rules.SeverityWarn, now)
	if len(g.applied) != 0 || g.evals != 1 || !strings.Contains(out, "import-cycle") {
		t.Fatalf("applied=%v evals=%d out=%q", g.applied, g.evals, out)
	}
}

// Without a session the debt added cannot be told apart: never fail.
func TestGateWithoutSessionPasses(t *testing.T) {
	g := &fakeGraph{states: [][]rules.Finding{{f("import-cycle", "error", "c")}}}
	if out, err := gateText(context.Background(), g.deps(&fakeSessions{}), "", []string{"x.go"}, rules.SeverityWarn, time.Now()); out != "" || err != nil {
		t.Fatalf("RunGate = %q, %v", out, err)
	}
}

func TestGateApplyErrorIsReturned(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	s := &fakeSessions{}
	_ = s.PutBaseline(ctx, "s1", nil, now)
	g := &fakeGraph{states: [][]rules.Finding{{}}, err: errors.New("boom")}
	if _, err := gateText(ctx, g.deps(s), "s1", []string{"x.go"}, rules.SeverityWarn, now); err == nil {
		t.Fatal("want the apply error")
	}
}

// gateText runs RunGate and renders the result.
func gateText(ctx context.Context, d GateDeps, session string, files []string, floor rules.Severity, now time.Time) (string, error) {
	added, err := RunGate(ctx, d, session, files, floor, now)
	return RenderGate(added), err
}

func TestRenderGateLimitsLines(t *testing.T) {
	var fs []rules.Finding
	for range 30 {
		fs = append(fs, f("unused-export", "warn", "F"))
	}
	out := RenderGate(fs)
	if !strings.Contains(out, "30 new finding") || !strings.Contains(out, "and 10 more") {
		t.Fatalf("RenderGate:\n%s", out)
	}
}
