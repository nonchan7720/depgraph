package hook

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// GateDeps are what RunGate needs from the project's graph.
type GateDeps struct {
	Sessions SessionStore
	// Apply puts the changed files (repo-relative; missing ones are deleted)
	// into the graph.
	Apply func(ctx context.Context, files []string) error
	// Evaluate runs the rules on the graph.
	Evaluate func(ctx context.Context) ([]rules.Finding, error)
}

// maxGateLines is how many findings RenderGate lists.
const maxGateLines = 20

// RunGate is the check of a quality gate (claude-hook-gate) after an edit or
// before the agent stops: it applies files to the graph and returns the
// findings at least floor that the session added since its baseline (most
// severe first; render them with RenderGate). The baseline is recorded by session-start; without
// one, the findings before files are applied become the baseline. Without a
// session nothing can be told apart and it passes.
func RunGate(ctx context.Context, d GateDeps, session string, files []string, floor rules.Severity, now time.Time) ([]rules.Finding, error) {
	if session == "" || d.Sessions == nil {
		return nil, nil
	}
	base, ok, err := d.Sessions.Baseline(ctx, session)
	if err != nil {
		return nil, err
	}
	cur, evaluated := base, false
	if !ok {
		if base, err = d.Evaluate(ctx); err != nil {
			return nil, err
		}
		if err := d.Sessions.PutBaseline(ctx, session, base, now); err != nil {
			return nil, err
		}
		cur, evaluated = base, true
	}
	if len(files) > 0 {
		if err := d.Apply(ctx, files); err != nil {
			return nil, err
		}
		evaluated = false
	}
	if !evaluated {
		if cur, err = d.Evaluate(ctx); err != nil {
			return nil, err
		}
	}
	return rules.NewSince(base, cur, floor), nil
}

// RenderGate lists the findings added in the session, for the agent ("" when
// there are none).
func RenderGate(added []rules.Finding) string {
	if len(added) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "depgraph: %d new finding(s) in this session. Fix them before finishing "+
		"(reuse or remove the code instead of adding more), or say why one is intended:\n", len(added))
	for i, f := range added {
		if i == maxGateLines {
			fmt.Fprintf(&b, "- ... and %d more\n", len(added)-maxGateLines)
			break
		}
		fmt.Fprintf(&b, "- [%s] %s: %s\n", f.Severity, f.Rule, f.Message)
	}
	return b.String()
}
