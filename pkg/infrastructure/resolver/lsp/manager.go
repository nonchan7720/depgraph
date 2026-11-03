package lsp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
)

// Session is a running language server the Manager can query and stop.
type Session interface {
	Server
	DidChangeWatchedFiles(changes []FileChange) error
	Shutdown(ctx context.Context) error
}

// LaunchFunc starts and initializes a language server for lang.
type LaunchFunc func(ctx context.Context, root string, lang graph.Language) (Session, error)

// Manager starts language servers lazily (on the first file of a language) and
// shares one server between languages that use the same command (TS and JS).
// A server that cannot start is reported once through Warn and then disabled.
type Manager struct {
	Root    string
	Symbols SymbolsFunc
	Launch  LaunchFunc                    // defaults to DefaultLaunch
	Warn    func(format string, a ...any) // defaults to a no-op

	mu       sync.Mutex
	sessions map[string]*managed // key: server executable
}

type managed struct {
	sess     Session // nil when disabled
	enricher *Enricher
}

// NewManager returns a Manager that launches real servers.
func NewManager(root string, symbols SymbolsFunc, warn func(string, ...any)) *Manager {
	return &Manager{Root: root, Symbols: symbols, Launch: DefaultLaunch, Warn: warn}
}

// DefaultLaunch runs ServerCommand(lang) and performs the initialize handshake.
// The process is deliberately not bound to ctx so Manager.Close can shut it
// down gracefully after ctx is cancelled.
func DefaultLaunch(ctx context.Context, root string, lang graph.Language) (Session, error) {
	cmd := ServerCommand(lang)
	if len(cmd) == 0 {
		return nil, fmt.Errorf("no language server for %s", lang)
	}
	if _, err := exec.LookPath(cmd[0]); err != nil {
		return nil, fmt.Errorf("%s not found in PATH", cmd[0])
	}
	c, err := Start(context.WithoutCancel(ctx), root, cmd)
	if err != nil {
		return nil, err
	}
	ictx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := c.Initialize(ictx); err != nil {
		kctx, kcancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer kcancel()
		_ = c.Shutdown(kctx)
		return nil, fmt.Errorf("initialize %s: %w", cmd[0], err)
	}
	return c, nil
}

// get returns the managed server for lang, launching it on first use. It
// returns nil when the language has no server or the server is disabled.
func (m *Manager) get(ctx context.Context, lang graph.Language) *managed {
	cmd := ServerCommand(lang)
	if len(cmd) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions == nil {
		m.sessions = map[string]*managed{}
	}
	if s, ok := m.sessions[cmd[0]]; ok {
		return s
	}
	launch := m.Launch
	if launch == nil {
		launch = DefaultLaunch
	}
	s := &managed{}
	sess, err := launch(ctx, m.Root, lang)
	if err != nil {
		if m.Warn != nil {
			m.Warn("lsp: %s disabled: %v", cmd[0], err)
		}
	} else {
		s.sess = sess
		s.enricher = &Enricher{Server: sess, Root: m.Root, Symbols: m.Symbols}
	}
	m.sessions[cmd[0]] = s
	return s
}

// Enrich fills fg.Refs through the server for fg.Language. With no usable
// server it does nothing and returns nil.
func (m *Manager) Enrich(ctx context.Context, fg *graph.FileGraph) error {
	s := m.get(ctx, fg.Language)
	if s == nil || s.sess == nil {
		return nil
	}
	return s.enricher.Enrich(ctx, fg)
}

// NotifyChanges tells every running server about on-disk changes so it drops
// stale caches. Servers that are not running are not started.
func (m *Manager) NotifyChanges(changes []FileChange) {
	m.mu.Lock()
	var live []Session
	for _, s := range m.sessions {
		if s.sess != nil {
			live = append(live, s.sess)
		}
	}
	m.mu.Unlock()
	for _, s := range live {
		_ = s.DidChangeWatchedFiles(changes)
	}
}

// Close shuts down every running server.
func (m *Manager) Close(ctx context.Context) {
	m.mu.Lock()
	sessions := m.sessions
	m.sessions = nil
	m.mu.Unlock()
	for _, s := range sessions {
		if s.sess != nil {
			_ = s.sess.Shutdown(ctx)
		}
	}
}

// ChangesFor classifies changed repo-relative files for didChangeWatchedFiles:
// files that no longer exist are deletions, everything else a change.
func ChangesFor(root string, files []string) []FileChange {
	out := make([]FileChange, 0, len(files))
	for _, f := range files {
		t := FileChanged
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(f))); err != nil {
			t = FileDeleted
		}
		out = append(out, FileChange{Rel: f, Type: t})
	}
	return out
}

// FilesChanged tells the running servers about changed files
// (implements analysis.Refinement together with Enrich).
func (m *Manager) FilesChanged(files []string) { m.NotifyChanges(ChangesFor(m.Root, files)) }
