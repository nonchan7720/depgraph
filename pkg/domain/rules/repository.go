package rules

import "context"

// Repository evaluates the rules against the stored dependency graph.
type Repository interface {
	Evaluate(ctx context.Context, cfg Config) ([]Finding, error)
}
