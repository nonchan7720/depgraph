// Package analysis is the application service: it keeps the dependency graph
// in the store in sync with the repository (full scan, incremental changes,
// optional LSP refinement) and evaluates the architectural rules.
package analysis

import (
	"context"
	"io"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// Service keeps the stored graph in sync with the repository.
type Service interface {
	// FullScan parses every supported file, upserts it, and removes files
	// left from earlier runs (deleted, newly ignored, other root).
	FullScan(ctx context.Context) error
	// HandleChanges applies changed files (repo-relative; missing ones are
	// deleted), queues them for refinement and evaluates the rules.
	HandleChanges(ctx context.Context, changed []string) error
	// Evaluate runs the rules, logs the diff against the previous run and
	// publishes the result (notifier, report).
	Evaluate(ctx context.Context) error
	// SetNotifier registers a receiver of each evaluation (the UI). nil disables it.
	SetNotifier(n Notifier)
	// StartRefinement refines changed files in the background with r until
	// ctx is cancelled. The returned func waits for the worker to stop.
	StartRefinement(ctx context.Context, r Refinement) (wait func())
}

// Notifier receives each evaluation result.
type Notifier interface {
	SetFindings(stats graph.Stats, findings []rules.Finding)
}

// ReportWriter persists each evaluation (CLAUDE.md managed section).
type ReportWriter interface {
	Write(stats graph.Stats, findings []rules.Finding) error
}

// Refinement adds precise symbol-level references to a FileGraph (LSP).
type Refinement interface {
	Enrich(ctx context.Context, fg *graph.FileGraph) error
	// FilesChanged tells the backend which files changed on disk.
	FilesChanged(files []string)
}

// ScanFunc walks root and calls fn for every supported, non-ignored file.
type ScanFunc func(ctx context.Context, r graph.Resolver, root string, fn func(*graph.FileGraph) error) error

// Deps are the service's collaborators.
type Deps struct {
	Repo     graph.Repository
	Rules    rules.Repository
	Resolver graph.Resolver
	Scan     ScanFunc
	Report   ReportWriter // nil: no report
	Out      io.Writer    // progress log (nil: discarded)
	// Config defaults to rules.DefaultConfig().
	Config *rules.Config
}

// Options are per-run settings.
type Options struct {
	Root  string // absolute repository root
	Reset bool   // clear the store before FullScan
}
