package neo4jprovider

import (
	"context"
	"errors"
)

// DefaultExternalURI is used when the external provider is named explicitly
// but no URI is configured (the default of releases before providers existed).
const DefaultExternalURI = "bolt://localhost:7687"

// ExternalProvider is a Neo4j the user runs (docker compose, a server, Aura):
// depgraph connects and never starts, stops or removes it.
type ExternalProvider struct {
	uri, user, pass, db string
}

// NewExternal returns the external provider for the configured connection.
// uri "" means not configured.
func NewExternal(uri, user, pass, db string) ExternalProvider {
	return ExternalProvider{uri: uri, user: user, pass: pass, db: db}
}

// WithDefaultURI returns e with DefaultExternalURI when no URI is configured.
func (e ExternalProvider) WithDefaultURI() ExternalProvider {
	if e.uri == "" {
		e.uri = DefaultExternalURI
	}
	return e
}

func (e ExternalProvider) Name() string { return External }

func (e ExternalProvider) Available(context.Context) error {
	if e.uri == "" {
		return errors.New("no URI configured (--neo4j / NEO4J_URI)")
	}
	return nil
}

func (e ExternalProvider) conn() Conn {
	return Conn{URI: e.uri, User: e.user, Password: e.pass, Database: e.db, Describe: e.uri}
}

// Provide returns the configured server whatever the policy: it is already
// running or the connection fails.
func (e ExternalProvider) Provide(context.Context, StoreRequest) (Conn, error) {
	return e.conn(), nil
}

// Discover returns the configured server.
func (e ExternalProvider) Discover(context.Context, string) (Conn, bool, error) {
	return e.conn(), e.uri != "", nil
}
