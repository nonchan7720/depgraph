// Package neo4jprovider decides how depgraph gets a Neo4j server: an
// external one the user runs (external), one depgraph starts and cleans up in
// a container (container, subpackage container), and later a local Neo4j
// installation run as a child process (local, planned). Which runtime or
// installation exists is each provider's own business; this package only
// defines the contract and the selection order.
package neo4jprovider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Provider names, in the order Select tries them.
const (
	External  = "external"
	Container = "container"
	Local     = "local"
)

// Policy is what Provide may do when no Neo4j serves the root yet.
type Policy int

const (
	// Existing never starts anything: only a running Neo4j is used (hook).
	Existing Policy = iota
	// StartIfPersistent starts one only when StoreRequest.Persist is set: a fresh
	// ephemeral store would be empty (serve, projects).
	StartIfPersistent
	// Start starts one when none serves the root (scan, check, watch).
	Start
)

// StoreRequest is what a command asks a provider for.
type StoreRequest struct {
	// Root is the absolute repository root the store belongs to.
	Root string
	// Project is root's project key (names a persistent store).
	Project string
	// Persist keeps the data across runs; otherwise it is gone when the
	// process that started the store exits.
	Persist bool
	Policy  Policy
	// Ready reports whether Neo4j at c accepts connections; a provider that
	// starts (or finds a starting) Neo4j polls it before returning.
	Ready func(ctx context.Context, c Conn) error
	// Log receives progress lines (stderr); nil discards them.
	Log io.Writer
}

// Logf writes one progress line to o.Log.
func (o StoreRequest) Logf(format string, args ...any) {
	if o.Log != nil {
		_, _ = fmt.Fprintf(o.Log, format+"\n", args...)
	}
}

// Conn is a Neo4j to connect to.
type Conn struct {
	URI, User, Password, Database string
	// Started: this process started it, so Release stops it (an ephemeral
	// store is removed with its data; a persistent one is only stopped).
	Started bool
	// Persistent: its data outlives the process (the process does not).
	Persistent bool
	// Describe names it for messages, e.g. "container depgraph-app-1a2b3c4d (docker)".
	Describe string
	release  func(ctx context.Context) error
}

// WithRelease returns c with the function Release calls.
func (c Conn) WithRelease(fn func(ctx context.Context) error) Conn {
	c.release = fn
	return c
}

// Release gives back what Provide acquired: a store this process started is
// stopped (ephemeral: removed with its data; persistent: stopped, data and
// container kept for the next start); a store it only reused is left running.
func (c Conn) Release(ctx context.Context) error {
	if c.release == nil {
		return nil
	}
	return c.release(ctx)
}

// Provider provides Neo4j. Contract:
//   - Available is cheap enough for every command (no side effects) and
//     returns a one-line reason when the provider cannot be used here.
//   - Provide reuses a Neo4j already serving StoreRequest.Root; otherwise it
//     starts one as StoreRequest.Policy allows, or returns ErrNotRunning.
//   - Discover is read-only: it never starts, stops or removes anything and
//     prints nothing (the Claude Code hook uses it).
type Provider interface {
	Name() string
	Available(ctx context.Context) error
	Provide(ctx context.Context, o StoreRequest) (Conn, error)
	Discover(ctx context.Context, root string) (Conn, bool, error)
}

// ErrNotRunning: no Neo4j serves the root and the policy does not allow
// starting one.
var ErrNotRunning = errors.New("no running Neo4j for this root")

// Select returns the provider to use: the one named by explicit (flag,
// environment or .depgraph.yml) when set, otherwise the first available one
// in the given order. The error is one line and names, for every provider,
// what is missing.
func Select(ctx context.Context, explicit string, providers []Provider) (Provider, error) {
	if explicit != "" {
		var names []string
		for _, p := range providers {
			names = append(names, p.Name())
			if p.Name() != explicit {
				continue
			}
			if err := p.Available(ctx); err != nil {
				return nil, fmt.Errorf("Neo4j provider %s: %w", explicit, err)
			}
			return p, nil
		}
		return nil, fmt.Errorf("unknown Neo4j provider %q (known: %s)", explicit, strings.Join(names, ", "))
	}
	var reasons []string
	for _, p := range providers {
		err := p.Available(ctx)
		if err == nil {
			return p, nil
		}
		reasons = append(reasons, p.Name()+": "+oneLine(err.Error()))
	}
	return nil, fmt.Errorf("no Neo4j available (%s)", strings.Join(reasons, "; "))
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// planned is a provider that is not implemented yet: it is never available,
// and naming it explains so.
type planned struct{ name, reason string }

// Planned returns a provider that is listed (in errors and as a valid name)
// but never available.
func Planned(name, reason string) Provider { return planned{name: name, reason: reason} }

func (p planned) Name() string { return p.name }
func (p planned) Available(context.Context) error {
	return errors.New(p.reason)
}
func (p planned) Provide(context.Context, StoreRequest) (Conn, error) {
	return Conn{}, fmt.Errorf("Neo4j provider %s: %s", p.name, p.reason)
}
func (p planned) Discover(context.Context, string) (Conn, bool, error) {
	return Conn{}, false, nil
}
