package graph

import (
	"context"
	"fmt"
)

// deleteBatch is how many relationships or nodes one delete transaction
// removes. A whole repository in one transaction exceeds Neo4j's
// transaction memory (a 14,000-file repository needs more than 700 MiB):
// `--reset` and `projects rm` then failed.
var deleteBatch = 10000

// Writer runs a write statement and returns its rows.
type Writer interface {
	Write(ctx context.Context, cypher string, params map[string]any) ([]map[string]any, error)
}

// projectNodes matches the nodes n that hold the graph data of $project.
const projectNodes = `MATCH (n) WHERE (n:Package OR n:File OR n:Symbol OR n:HookSession OR n:Function OR n:Module OR n:Type OR n:Use OR n:Part OR n:Root OR n:ExtImport OR n:Dependency OR n:LinkState) AND n.project = $project`

// DeleteProjectData deletes the graph data of project key (packages,
// files, symbols, hook sessions, the linked graph; not the Project node), a
// batch per transaction.
//
// The relationships go first and the nodes after them. A node can have any
// number of relationships (a function many call, a package every file
// imports), so a batch of nodes deleted with their relationships is not a
// bounded transaction: the linked graph of a large repository went over the
// transaction memory with it. Every relationship of the data starts at one
// of its nodes; DETACH covers what else may point at a node.
func DeleteProjectData(ctx context.Context, w Writer, key string) error {
	for _, stmt := range []string{
		projectNodes + ` MATCH (n)-[r]->() WITH r LIMIT $batch DELETE r RETURN count(*) AS deleted`,
		projectNodes + ` WITH n LIMIT $batch DETACH DELETE n RETURN count(*) AS deleted`,
	} {
		for {
			rows, err := w.Write(ctx, stmt, map[string]any{"project": key, "batch": deleteBatch})
			if err != nil {
				return fmt.Errorf("delete project %s: %w", key, err)
			}
			if len(rows) == 0 {
				break
			}
			if n, _ := rows[0]["deleted"].(int64); n < int64(deleteBatch) {
				break
			}
		}
	}
	return nil
}
