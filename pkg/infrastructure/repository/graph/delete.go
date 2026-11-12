package graph

import (
	"context"
	"fmt"
)

// deleteBatch is how many nodes one delete transaction removes. A whole
// repository in one transaction exceeds Neo4j's transaction memory (a
// 14,000-file repository needs more than 700 MiB): `--reset` and
// `projects rm` then failed.
var deleteBatch = 10000

// Writer runs a write statement and returns its rows.
type Writer interface {
	Write(ctx context.Context, cypher string, params map[string]any) ([]map[string]any, error)
}

// DeleteProjectData deletes the graph data of project key (packages,
// files, symbols, hook sessions; not the Project node), a batch per
// transaction.
func DeleteProjectData(ctx context.Context, w Writer, key string) error {
	for {
		rows, err := w.Write(ctx, `
			MATCH (n) WHERE (n:Package OR n:File OR n:Symbol OR n:HookSession) AND n.project = $project
			WITH n LIMIT $batch
			DETACH DELETE n
			RETURN count(*) AS deleted
		`, map[string]any{"project": key, "batch": deleteBatch})
		if err != nil {
			return fmt.Errorf("delete project %s: %w", key, err)
		}
		if len(rows) == 0 {
			return nil
		}
		if n, _ := rows[0]["deleted"].(int64); n < int64(deleteBatch) {
			return nil
		}
	}
}
