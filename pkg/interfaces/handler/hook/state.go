package hook

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// SessionStore keeps, per hook session, the findings last injected, so a
// prompt gets only what changed since. It lives in the graph store with the
// project's other data (removed with it), never in the repository.
type SessionStore interface {
	// Get returns the session's findings; ok is false for an unknown session.
	Get(ctx context.Context, session string) (findings []rules.Finding, ok bool, err error)
	// Put replaces the session's findings.
	Put(ctx context.Context, session string, findings []rules.Finding, now time.Time) error
	// Baseline returns the findings the session started with (the gate's
	// reference); ok is false when none was recorded.
	Baseline(ctx context.Context, session string) (findings []rules.Finding, ok bool, err error)
	// PutBaseline records the session's baseline unless it has one already.
	PutBaseline(ctx context.Context, session string, findings []rules.Finding, now time.Time) error
}

// legacyStateFile is where versions up to 0.1 kept the session state, inside
// the repository. It is removed when the hook runs.
const legacyStateFile = ".claude/.depgraph-hook-state.json"

// removeLegacyState deletes the state file of earlier versions under root.
func removeLegacyState(root string) {
	_ = os.Remove(filepath.Join(root, filepath.FromSlash(legacyStateFile)))
}
