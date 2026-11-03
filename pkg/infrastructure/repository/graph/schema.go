package graph

import (
	"errors"
	"strings"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// isSchemaRace reports whether err only means that another session changed
// the same schema rule at the same moment. `IF NOT EXISTS` / `IF EXISTS` do
// not protect against that: when two sessions create (or drop) the same
// index or constraint concurrently, the slower one still fails.
func isSchemaRace(stmt string, err error) bool {
	var ne *neo4j.Neo4jError
	if err == nil || !errors.As(err, &ne) {
		return false
	}
	switch {
	case strings.HasPrefix(strings.TrimSpace(stmt), "CREATE"):
		switch ne.Code {
		case "Neo.ClientError.Schema.EquivalentSchemaRuleAlreadyExists",
			"Neo.ClientError.Schema.ConstraintAlreadyExists",
			"Neo.ClientError.Schema.IndexAlreadyExists":
			return true
		}
	case strings.HasPrefix(strings.TrimSpace(stmt), "DROP"):
		switch ne.Code {
		case "Neo.ClientError.Schema.ConstraintDropFailed",
			"Neo.ClientError.Schema.IndexDropFailed",
			"Neo.ClientError.Schema.ConstraintNotFound",
			"Neo.ClientError.Schema.IndexNotFound":
			return true
		}
	}
	return false
}
