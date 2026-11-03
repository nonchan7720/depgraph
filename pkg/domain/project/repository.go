package project

import "context"

// Repository stores the Project nodes.
type Repository interface {
	// Get returns the project stored under key, or nil when there is none.
	Get(ctx context.Context, key string) (*Project, error)
	// Upsert creates or replaces the project's properties (Files is ignored).
	Upsert(ctx context.Context, p Project) error
	// List returns every project ordered by key, with Files filled in.
	List(ctx context.Context) ([]Project, error)
	// Delete removes the project and all of its graph data.
	Delete(ctx context.Context, key string) error
}
