package graph

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"
)

func TestIsSchemaRace(t *testing.T) {
	neoErr := func(code string) error { return &neo4j.Neo4jError{Code: code} }
	cases := []struct {
		name string
		stmt string
		err  error
		want bool
	}{
		{"nil", "CREATE INDEX x IF NOT EXISTS FOR (n:N) ON (n.a)", nil, false},
		{"equivalent index created concurrently", "CREATE INDEX x IF NOT EXISTS FOR (n:N) ON (n.a)",
			neoErr("Neo.ClientError.Schema.EquivalentSchemaRuleAlreadyExists"), true},
		{"constraint created concurrently", "CREATE CONSTRAINT c IF NOT EXISTS FOR (n:N) REQUIRE n.a IS UNIQUE",
			neoErr("Neo.ClientError.Schema.ConstraintAlreadyExists"), true},
		{"index created concurrently", "CREATE INDEX x IF NOT EXISTS FOR (n:N) ON (n.a)",
			neoErr("Neo.ClientError.Schema.IndexAlreadyExists"), true},
		{"wrapped", "CREATE INDEX x IF NOT EXISTS FOR (n:N) ON (n.a)",
			fmt.Errorf("ensure: %w", neoErr("Neo.ClientError.Schema.EquivalentSchemaRuleAlreadyExists")), true},
		{"constraint dropped concurrently", "DROP CONSTRAINT c IF EXISTS",
			neoErr("Neo.ClientError.Schema.ConstraintDropFailed"), true},
		{"index dropped concurrently", "DROP INDEX x IF EXISTS",
			neoErr("Neo.ClientError.Schema.IndexDropFailed"), true},
		{"drop codes do not excuse a create", "CREATE INDEX x IF NOT EXISTS FOR (n:N) ON (n.a)",
			neoErr("Neo.ClientError.Schema.IndexDropFailed"), false},
		{"create codes do not excuse a drop", "DROP INDEX x IF EXISTS",
			neoErr("Neo.ClientError.Schema.IndexAlreadyExists"), false},
		{"real failure", "CREATE INDEX x IF NOT EXISTS FOR (n:N) ON (n.a)",
			neoErr("Neo.ClientError.Statement.SyntaxError"), false},
		{"non-neo4j error", "CREATE INDEX x IF NOT EXISTS FOR (n:N) ON (n.a)", errors.New("connection refused"), false},
	}
	for _, c := range cases {
		if got := isSchemaRace(c.stmt, c.err); got != c.want {
			t.Errorf("%s: isSchemaRace = %v, want %v", c.name, got, c.want)
		}
	}
}

// EnsureSchema runs at the start of every command and every test package;
// several of them can hit an empty database at the same time.
func TestEnsureSchemaConcurrently(t *testing.T) {
	st, ctx := testStore(t)
	const n = 8
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- st.EnsureSchema(ctx)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent EnsureSchema: %v", err)
		}
	}
}
