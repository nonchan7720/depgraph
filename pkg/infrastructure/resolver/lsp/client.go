// Package lsp is the incremental, precise resolver backend. It drives a
// language server over stdio (JSON-RPC) and asks for references / call
// hierarchy of the symbols tree-sitter found in a changed file.
//
// The Client speaks JSON-RPC; Enricher turns call hierarchy / references into
// FileGraph.Refs; Manager starts servers lazily per language. pkg/interfaces/cmd
// plugs the Manager into the watch loop.
package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
)

// ServerCommand returns the default language-server command for a language.
func ServerCommand(lang graph.Language) []string {
	switch lang {
	case graph.Go:
		return []string{"gopls", "serve"}
	case graph.TypeScript, graph.JavaScript:
		return []string{"typescript-language-server", "--stdio"}
	case graph.Python:
		return []string{"pyright-langserver", "--stdio"}
	}
	return nil
}

// Client is a minimal JSON-RPC 2.0 client over a server's stdio.
type Client struct {
	cmd    *exec.Cmd // nil when built from raw pipes (tests)
	stdin  io.WriteCloser
	stdout *bufio.Reader
	wmu    sync.Mutex
	nextID atomic.Int64

	pmu     sync.Mutex
	pending map[string]chan rpcResponse // key: normalized response id
	root    string

	done chan struct{} // closed when the read loop ends (server gone)

	vmu      sync.Mutex
	versions map[string]int // document version counter per rel path
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      *int64 `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// rpcMessage is anything the server can send: a response (ID, no Method), a
// request (ID and Method) or a notification (Method only).
type rpcMessage struct {
	ID     json.RawMessage `json:"id"` // number or string
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	Result json.RawMessage
	Error  *rpcError
}

// Start launches the server process and the read loop.
func Start(ctx context.Context, root string, command []string) (*Client, error) {
	if len(command) == 0 {
		return nil, fmt.Errorf("lsp: no server command")
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = root
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("lsp: start %s: %w", command[0], err)
	}
	c := newClient(stdout, stdin, root)
	c.cmd = cmd
	return c, nil
}

// newClient builds a client over arbitrary streams and starts the read loop.
// Start uses it with the server's stdio; tests use it with io.Pipe.
func newClient(r io.Reader, w io.WriteCloser, root string) *Client {
	c := &Client{
		stdin: w, stdout: bufio.NewReaderSize(r, 1<<20),
		pending: map[string]chan rpcResponse{}, root: root,
		done: make(chan struct{}), versions: map[string]int{},
	}
	go c.readLoop()
	return c
}

// idKey normalizes a JSON-RPC id (number or string) into a map key so that
// 1 and "1" refer to the same request.
func idKey(raw json.RawMessage) string {
	return strings.Trim(strings.TrimSpace(string(raw)), `"`)
}

func (c *Client) readLoop() {
	defer close(c.done)
	for {
		contentLen := 0
		for {
			line, err := c.stdout.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			if v, ok := strings.CutPrefix(line, "Content-Length: "); ok {
				contentLen, _ = strconv.Atoi(strings.TrimSpace(v))
			}
		}
		if contentLen <= 0 {
			continue
		}
		body := make([]byte, contentLen)
		if _, err := io.ReadFull(c.stdout, body); err != nil {
			return
		}
		var msg rpcMessage
		if err := json.Unmarshal(body, &msg); err != nil {
			continue
		}
		hasID := len(msg.ID) > 0 && string(msg.ID) != "null"
		switch {
		case msg.Method != "" && hasID:
			// server -> client request: must be answered or the server stalls
			go c.answerServerRequest(msg)
		case msg.Method != "":
			// notification (logMessage, publishDiagnostics, $/progress ...): ignored
		case hasID:
			key := idKey(msg.ID)
			c.pmu.Lock()
			ch, ok := c.pending[key]
			delete(c.pending, key)
			c.pmu.Unlock()
			if ok {
				ch <- rpcResponse{Result: msg.Result, Error: msg.Error}
			}
		}
	}
}

// answerServerRequest replies to the few requests servers send during startup.
func (c *Client) answerServerRequest(msg rpcMessage) {
	reply := map[string]any{"jsonrpc": "2.0", "id": msg.ID}
	switch msg.Method {
	case "workspace/configuration":
		var p struct {
			Items []json.RawMessage `json:"items"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		reply["result"] = make([]any, len(p.Items)) // one null per item
	case "client/registerCapability", "client/unregisterCapability", "window/workDoneProgress/create":
		reply["result"] = nil
	default:
		reply["error"] = rpcError{Code: -32601, Message: "method not found: " + msg.Method}
	}
	_ = c.write(reply)
}

func (c *Client) write(msg any) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n", len(b)); err != nil {
		return err
	}
	_, err = c.stdin.Write(b)
	return err
}

// Call sends a request and decodes the result into out.
func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	id := c.nextID.Add(1)
	key := strconv.FormatInt(id, 10)
	ch := make(chan rpcResponse, 1)
	c.pmu.Lock()
	c.pending[key] = ch
	c.pmu.Unlock()
	forget := func() {
		c.pmu.Lock()
		delete(c.pending, key)
		c.pmu.Unlock()
	}

	if err := c.write(rpcRequest{JSONRPC: "2.0", ID: &id, Method: method, Params: params}); err != nil {
		forget()
		return err
	}
	select {
	case <-ctx.Done():
		forget()
		return ctx.Err()
	case <-c.done:
		forget()
		return fmt.Errorf("lsp %s: server connection closed", method)
	case resp := <-ch:
		if resp.Error != nil {
			return fmt.Errorf("lsp %s: %s", method, resp.Error.Message)
		}
		if out != nil && len(resp.Result) > 0 && string(resp.Result) != "null" {
			return json.Unmarshal(resp.Result, out)
		}
		return nil
	}
}

// Notify sends a notification (no response).
func (c *Client) Notify(method string, params any) error {
	return c.write(rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
}

// Initialize performs the LSP handshake.
func (c *Client) Initialize(ctx context.Context) error {
	params := map[string]any{
		"processId": nil,
		"rootUri":   fileURI(c.root),
		"workspaceFolders": []any{
			map[string]any{"uri": fileURI(c.root), "name": filepath.Base(c.root)},
		},
		"capabilities": map[string]any{
			"workspace": map[string]any{
				"configuration":         true,
				"workspaceFolders":      true,
				"didChangeWatchedFiles": map[string]any{"dynamicRegistration": false},
			},
			"window": map[string]any{"workDoneProgress": true},
			"textDocument": map[string]any{
				"synchronization": map[string]any{"dynamicRegistration": false},
				"references":      map[string]any{},
				"callHierarchy":   map[string]any{},
			},
		},
	}
	if err := c.Call(ctx, "initialize", params, nil); err != nil {
		return err
	}
	return c.Notify("initialized", map[string]any{})
}

// Position is a 0-based line / UTF-16 character offset.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range is an LSP range.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location is an LSP location.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// CallHierarchyItem is the LSP item returned by prepareCallHierarchy. Data is
// opaque server state and must be sent back unchanged.
type CallHierarchyItem struct {
	Name           string          `json:"name"`
	Kind           int             `json:"kind"`
	URI            string          `json:"uri"`
	Range          Range           `json:"range"`
	SelectionRange Range           `json:"selectionRange"`
	Data           json.RawMessage `json:"data,omitempty"`
}

// OutgoingCall is a call made by the queried item.
type OutgoingCall struct {
	To         CallHierarchyItem `json:"to"`
	FromRanges []Range           `json:"fromRanges"`
}

// IncomingCall is a call to the queried item.
type IncomingCall struct {
	From       CallHierarchyItem `json:"from"`
	FromRanges []Range           `json:"fromRanges"`
}

func (c *Client) absURI(rel string) string {
	return fileURI(filepath.Join(c.root, filepath.FromSlash(rel)))
}

// References asks for all references to the symbol at (line, char) in rel.
// line/char are 0-based.
func (c *Client) References(ctx context.Context, rel string, line, char int) ([]Location, error) {
	var locs []Location
	err := c.Call(ctx, "textDocument/references", map[string]any{
		"textDocument": map[string]any{"uri": c.absURI(rel)},
		"position":     map[string]any{"line": line, "character": char},
		"context":      map[string]any{"includeDeclaration": false},
	}, &locs)
	return locs, err
}

// PrepareCallHierarchy returns the call hierarchy items at (line, char) in rel
// (0-based). An empty slice means there is no callable at that position.
func (c *Client) PrepareCallHierarchy(ctx context.Context, rel string, line, char int) ([]CallHierarchyItem, error) {
	var items []CallHierarchyItem
	err := c.Call(ctx, "textDocument/prepareCallHierarchy", map[string]any{
		"textDocument": map[string]any{"uri": c.absURI(rel)},
		"position":     map[string]any{"line": line, "character": char},
	}, &items)
	return items, err
}

// OutgoingCalls returns the calls made from item.
func (c *Client) OutgoingCalls(ctx context.Context, item CallHierarchyItem) ([]OutgoingCall, error) {
	var out []OutgoingCall
	err := c.Call(ctx, "callHierarchy/outgoingCalls", map[string]any{"item": item}, &out)
	return out, err
}

// IncomingCalls returns the calls made to item.
func (c *Client) IncomingCalls(ctx context.Context, item CallHierarchyItem) ([]IncomingCall, error) {
	var out []IncomingCall
	err := c.Call(ctx, "callHierarchy/incomingCalls", map[string]any{"item": item}, &out)
	return out, err
}

// LanguageID returns the LSP languageId for a file.
func LanguageID(rel string, lang graph.Language) string {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".tsx":
		return "typescriptreact"
	case ".jsx":
		return "javascriptreact"
	}
	return string(lang)
}

// DidOpen tells the server that rel is open, sending its current on-disk text.
func (c *Client) DidOpen(rel string, lang graph.Language) error {
	text, err := os.ReadFile(filepath.Join(c.root, filepath.FromSlash(rel)))
	if err != nil {
		return err
	}
	c.vmu.Lock()
	c.versions[rel]++
	v := c.versions[rel]
	c.vmu.Unlock()
	return c.Notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri": c.absURI(rel), "languageId": LanguageID(rel, lang), "version": v, "text": string(text),
		},
	})
}

// DidClose tells the server that rel is no longer open.
func (c *Client) DidClose(rel string) error {
	return c.Notify("textDocument/didClose", map[string]any{
		"textDocument": map[string]any{"uri": c.absURI(rel)},
	})
}

// FileChangeType is the LSP FileChangeType.
type FileChangeType int

// FileChangeType values.
const (
	FileCreated FileChangeType = 1
	FileChanged FileChangeType = 2
	FileDeleted FileChangeType = 3
)

// FileChange is one on-disk change of a repo-relative file.
type FileChange struct {
	Rel  string
	Type FileChangeType
}

// DidChangeWatchedFiles tells the server that files changed on disk, so it
// drops stale caches (we are the file watcher; servers rely on this).
func (c *Client) DidChangeWatchedFiles(changes []FileChange) error {
	cs := make([]map[string]any, 0, len(changes))
	for _, ch := range changes {
		cs = append(cs, map[string]any{"uri": c.absURI(ch.Rel), "type": int(ch.Type)})
	}
	return c.Notify("workspace/didChangeWatchedFiles", map[string]any{"changes": cs})
}

// Shutdown stops the server. A process that does not exit within ctx is killed.
func (c *Client) Shutdown(ctx context.Context) error {
	_ = c.Call(ctx, "shutdown", nil, nil)
	_ = c.Notify("exit", nil)
	_ = c.stdin.Close()
	if c.cmd == nil {
		return nil
	}
	exited := make(chan error, 1)
	go func() { exited <- c.cmd.Wait() }()
	select {
	case err := <-exited:
		return err
	case <-ctx.Done():
		_ = c.cmd.Process.Kill()
		return errors.Join(ctx.Err(), <-exited)
	}
}

func fileURI(abs string) string {
	abs = filepath.ToSlash(abs)
	if !strings.HasPrefix(abs, "/") {
		abs = "/" + abs
	}
	return "file://" + abs
}
