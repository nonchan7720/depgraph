package view

import "context"

// Reader answers the read queries of one project's graph.
type Reader interface {
	Stats(ctx context.Context) (Stats, error)
	Overview(ctx context.Context) (*Overview, error)
	PackageDetail(ctx context.Context, key string) (*PackageDetail, error)
}
