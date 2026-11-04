// Package ui serves the browser UI: the embedded web app (web/ build output,
// Cytoscape + ELK, no external CDN) at /, a JSON API, and a
// Server-Sent Events stream that tells the page to refresh after each
// incremental analysis.
package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	"github.com/nonchan7720/depgraph/pkg/domain/view"
)

// Server is the UI HTTP server.
type Server struct {
	st   Reader
	root string

	mu       sync.RWMutex
	findings []rules.Finding
	stats    view.Stats
	updated  time.Time

	cmu     sync.Mutex
	clients map[chan struct{}]struct{}

	web fs.FS // built web UI (dist/), served at /
}

// Reader is the part of view.Reader the UI reads from.
type Reader interface {
	Overview(ctx context.Context) (*view.Overview, error)
	PackageDetail(ctx context.Context, key string) (*view.PackageDetail, error)
}

// New creates a server reading from st.
func New(st Reader, root string) *Server {
	return &Server{st: st, root: root, clients: map[chan struct{}]struct{}{}, web: embeddedWeb()}
}

// SetFindings records the latest rule results and notifies browsers.
func (s *Server) SetFindings(stats view.Stats, findings []rules.Finding) {
	s.mu.Lock()
	s.stats, s.findings, s.updated = stats, findings, time.Now()
	s.mu.Unlock()
	s.broadcast()
}

func (s *Server) broadcast() {
	s.cmu.Lock()
	defer s.cmu.Unlock()
	for ch := range s.clients {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /", webHandler(s.web))
	mux.HandleFunc("GET /api/graph", s.handleGraph)
	mux.HandleFunc("GET /api/package", s.handlePackage)
	mux.HandleFunc("GET /events", s.handleEvents)
	return mux
}

// ListenAndServe runs until ctx is done.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	srv := &http.Server{Addr: addr, Handler: s.Handler()}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// GraphResponse is what GET /api/graph (and the MCP App's graph_view tool) answers.
type GraphResponse struct {
	Root     string          `json:"root"`
	Updated  time.Time       `json:"updated"`
	Stats    view.Stats      `json:"stats"`
	View     View            `json:"view"`
	Findings []rules.Finding `json:"findings"`
}

// parseViewOptions reads ?depth=N&expand=a,b&ext=1&q=text.
func parseViewOptions(q url.Values) ViewOptions {
	o := ViewOptions{Query: q.Get("q"), External: q.Get("ext") == "1", Focus: strings.Trim(q.Get("focus"), "/")}
	o.Depth, _ = strconv.Atoi(q.Get("depth"))
	for e := range strings.SplitSeq(q.Get("expand"), ",") {
		if e = strings.TrimSpace(e); e != "" {
			o.Expanded = append(o.Expanded, e)
		}
	}
	return o
}

// Graph folds the current package graph and the latest findings with o.
func (s *Server) Graph(ctx context.Context, o ViewOptions) (*GraphResponse, error) {
	ov, err := s.st.Overview(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	resp := GraphResponse{Root: s.root, Updated: s.updated, Stats: s.stats, Findings: s.findings}
	s.mu.RUnlock()
	if resp.Findings == nil {
		resp.Findings = []rules.Finding{}
	}
	resp.View = BuildView(ov, resp.Findings, o)
	return &resp, nil
}

// Package returns the files, symbols and import neighbours of one package.
func (s *Server) Package(ctx context.Context, key string) (*view.PackageDetail, error) {
	return s.st.PackageDetail(ctx, key)
}

func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	resp, err := s.Graph(r.Context(), parseViewOptions(r.URL.Query()))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, resp)
}

func (s *Server) handlePackage(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}
	d, err := s.Package(r.Context(), key)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, d)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := make(chan struct{}, 1)
	s.cmu.Lock()
	s.clients[ch] = struct{}{}
	s.cmu.Unlock()
	defer func() {
		s.cmu.Lock()
		delete(s.clients, ch)
		s.cmu.Unlock()
	}()

	_, _ = fmt.Fprint(w, "event: hello\ndata: {}\n\n")
	flusher.Flush()
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			_, _ = fmt.Fprint(w, "event: update\ndata: {}\n\n")
			flusher.Flush()
		case <-keepalive.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
