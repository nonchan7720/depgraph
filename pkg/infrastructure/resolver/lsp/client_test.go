package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
)

// fakeServer は io.Pipe 越しに LSP サーバーを演じるテスト用の相手。
type fakeServer struct {
	t   *testing.T
	in  *bufio.Reader // クライアント -> サーバー
	out io.Writer     // サーバー -> クライアント
	wmu sync.Mutex
}

type inMsg struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
}

// newPair はクライアントと、それに繋がった偽サーバーを返す。
func newPair(t *testing.T, root string) (*Client, *fakeServer) {
	t.Helper()
	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	c := newClient(s2cR, c2sW, root)
	t.Cleanup(func() { _ = c2sW.Close(); _ = s2cW.Close() })
	return c, &fakeServer{t: t, in: bufio.NewReader(c2sR), out: s2cW}
}

func (s *fakeServer) send(v any) {
	s.t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		s.t.Fatal(err)
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := fmt.Fprintf(s.out, "Content-Length: %d\r\n\r\n%s", len(b), b); err != nil {
		s.t.Fatalf("fake server write: %v", err)
	}
}

func (s *fakeServer) reply(id json.RawMessage, result any) {
	s.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

// serve はメッセージごとに handler を呼ぶ。返り値 true で応答済み扱い。
func (s *fakeServer) serve(handler func(m inMsg) (result any, handled bool)) {
	go func() {
		for {
			m := s.readNoFatal()
			if m == nil {
				return
			}
			if len(m.ID) > 0 && m.Method != "" {
				res, ok := handler(*m)
				if ok {
					s.reply(m.ID, res)
				}
			} else if m.Method != "" {
				handler(*m)
			}
		}
	}()
}

func (s *fakeServer) readNoFatal() *inMsg {
	n := 0
	for {
		line, err := s.in.ReadString('\n')
		if err != nil {
			return nil
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if v, ok := strings.CutPrefix(line, "Content-Length: "); ok {
			n, _ = strconv.Atoi(v)
		}
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(s.in, body); err != nil {
		return nil
	}
	var m inMsg
	if json.Unmarshal(body, &m) != nil {
		return nil
	}
	return &m
}

func ctxT(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// 文字列 ID のサーバー→クライアント要求に、同じ ID で null / null 配列を返す。
func TestServerRequestsAreAnswered(t *testing.T) {
	c, srv := newPair(t, t.TempDir())
	go func() {
		srv.send(map[string]any{"jsonrpc": "2.0", "id": "cfg-1", "method": "workspace/configuration",
			"params": map[string]any{"items": []any{map[string]any{"section": "a"}, map[string]any{"section": "b"}}}})
		srv.send(map[string]any{"jsonrpc": "2.0", "id": 7, "method": "client/registerCapability",
			"params": map[string]any{"registrations": []any{}}})
		srv.send(map[string]any{"jsonrpc": "2.0", "id": "tok", "method": "window/workDoneProgress/create",
			"params": map[string]any{"token": "x"}})
		// 未知のメソッドは MethodNotFound エラーで返す（ハングさせない）。
		srv.send(map[string]any{"jsonrpc": "2.0", "id": 99, "method": "foo/bar"})
	}()
	_ = c

	got := map[string]inMsgResult{}
	for range 4 {
		m := srv.readResp()
		got[string(m.ID)] = m
	}
	if r := got[`"cfg-1"`]; string(r.Result) != "[null,null]" {
		t.Errorf("configuration result = %s", r.Result)
	}
	if r := got["7"]; string(r.Result) != "null" || r.Error != nil {
		t.Errorf("registerCapability = %s err=%v", r.Result, r.Error)
	}
	if r := got[`"tok"`]; string(r.Result) != "null" || r.Error != nil {
		t.Errorf("workDoneProgress/create = %s err=%v", r.Result, r.Error)
	}
	if r := got["99"]; r.Error == nil {
		t.Errorf("unknown method should return an error, got %s", r.Result)
	}
}

type inMsgResult struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code int `json:"code"`
	} `json:"error"`
}

func (s *fakeServer) readResp() inMsgResult {
	s.t.Helper()
	n := 0
	for {
		line, err := s.in.ReadString('\n')
		if err != nil {
			s.t.Fatalf("read: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if v, ok := strings.CutPrefix(line, "Content-Length: "); ok {
			n, _ = strconv.Atoi(v)
		}
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(s.in, body); err != nil {
		s.t.Fatalf("read body: %v", err)
	}
	var m inMsgResult
	if err := json.Unmarshal(body, &m); err != nil {
		s.t.Fatal(err)
	}
	return m
}

// 応答 ID が文字列 "1" で返ってきても対応するリクエストに届く。
func TestResponseWithStringID(t *testing.T) {
	c, srv := newPair(t, t.TempDir())
	go func() {
		m := srv.readResp()
		srv.send(map[string]any{"jsonrpc": "2.0", "id": strings.Trim(string(m.ID), `"`), "result": map[string]any{"ok": true}})
	}()
	var out struct{ OK bool }
	if err := c.Call(ctxT(t), "x", nil, &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK {
		t.Error("result not decoded")
	}
}

// サーバーが落ちたら待機中の Call は ctx を待たずにエラーで返る。
func TestCallFailsWhenServerGone(t *testing.T) {
	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	c := newClient(s2cR, c2sW, t.TempDir())
	go func() { _, _ = io.Copy(io.Discard, c2sR) }()
	go func() { time.Sleep(50 * time.Millisecond); _ = s2cW.Close() }()
	if err := c.Call(ctxT(t), "x", nil, nil); err == nil {
		t.Error("want error")
	}
}

func TestInitializeDidOpenCloseAndCallHierarchy(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, srv := newPair(t, root)

	var mu sync.Mutex
	var notes []string
	var openText, openLang string
	item := map[string]any{
		"name": "F", "kind": 12, "uri": fileURI(filepath.Join(root, "a.go")),
		"range":          rng(0, 0, 0, 5),
		"selectionRange": rng(0, 5, 0, 6),
		"data":           map[string]any{"k": "v"},
	}
	srv.serve(func(m inMsg) (any, bool) {
		mu.Lock()
		defer mu.Unlock()
		switch m.Method {
		case "initialize":
			var p struct {
				RootURI string `json:"rootUri"`
			}
			_ = json.Unmarshal(m.Params, &p)
			if p.RootURI != fileURI(root) {
				t.Errorf("rootUri = %s", p.RootURI)
			}
			return map[string]any{"capabilities": map[string]any{}}, true
		case "textDocument/didOpen":
			var p struct {
				TextDocument struct {
					Text       string `json:"text"`
					LanguageID string `json:"languageId"`
					Version    int    `json:"version"`
				} `json:"textDocument"`
			}
			_ = json.Unmarshal(m.Params, &p)
			openText, openLang = p.TextDocument.Text, p.TextDocument.LanguageID
			notes = append(notes, m.Method)
			return nil, false
		case "textDocument/didClose", "initialized":
			notes = append(notes, m.Method)
			return nil, false
		case "textDocument/prepareCallHierarchy":
			var p struct {
				Position struct{ Line, Character int }
			}
			_ = json.Unmarshal(m.Params, &p)
			if p.Position.Line != 3 || p.Position.Character != 5 {
				t.Errorf("position = %+v", p.Position)
			}
			return []any{item}, true
		case "callHierarchy/outgoingCalls":
			var p struct {
				Item struct {
					Name string
					Data map[string]string
				}
			}
			_ = json.Unmarshal(m.Params, &p)
			if p.Item.Name != "F" || p.Item.Data["k"] != "v" {
				t.Errorf("item not round-tripped: %+v", p.Item)
			}
			return []any{map[string]any{"to": item, "fromRanges": []any{rng(1, 1, 1, 2)}}}, true
		case "callHierarchy/incomingCalls":
			return []any{map[string]any{"from": item, "fromRanges": []any{}}}, true
		}
		return nil, false
	})

	ctx := ctxT(t)
	if err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.DidOpen("a.go", graph.Go); err != nil {
		t.Fatal(err)
	}
	items, err := c.PrepareCallHierarchy(ctx, "a.go", 3, 5)
	if err != nil || len(items) != 1 || items[0].Name != "F" {
		t.Fatalf("prepare = %+v, %v", items, err)
	}
	if items[0].SelectionRange.Start.Character != 5 {
		t.Errorf("selectionRange = %+v", items[0].SelectionRange)
	}
	out, err := c.OutgoingCalls(ctx, items[0])
	if err != nil || len(out) != 1 || out[0].To.Name != "F" {
		t.Fatalf("outgoing = %+v, %v", out, err)
	}
	in, err := c.IncomingCalls(ctx, items[0])
	if err != nil || len(in) != 1 || in[0].From.Name != "F" {
		t.Fatalf("incoming = %+v, %v", in, err)
	}
	if err := c.DidClose("a.go"); err != nil {
		t.Fatal(err)
	}
	// 通知は順序通りサーバーに届く: Shutdown までに全部読まれるよう少し待つ。
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(notes)
		mu.Unlock()
		if n >= 3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(notes, ",") != "initialized,textDocument/didOpen,textDocument/didClose" {
		t.Errorf("notes = %v", notes)
	}
	if openText != "package a\n" || openLang != "go" {
		t.Errorf("didOpen text=%q lang=%q", openText, openLang)
	}
}

func rng(sl, sc, el, ec int) map[string]any {
	return map[string]any{
		"start": map[string]any{"line": sl, "character": sc},
		"end":   map[string]any{"line": el, "character": ec},
	}
}

// prepareCallHierarchy が null を返したら空スライスで正常終了する。
func TestPrepareCallHierarchyNull(t *testing.T) {
	c, srv := newPair(t, t.TempDir())
	srv.serve(func(m inMsg) (any, bool) {
		if m.Method == "textDocument/prepareCallHierarchy" {
			return nil, true
		}
		return nil, false
	})
	items, err := c.PrepareCallHierarchy(ctxT(t), "a.go", 0, 0)
	if err != nil || len(items) != 0 {
		t.Errorf("items=%v err=%v", items, err)
	}
}

func TestDidChangeWatchedFiles(t *testing.T) {
	root := t.TempDir()
	c, srv := newPair(t, root)
	got := make(chan inMsg, 1)
	srv.serve(func(m inMsg) (any, bool) {
		if m.Method == "workspace/didChangeWatchedFiles" {
			got <- m
		}
		return nil, false
	})
	if err := c.DidChangeWatchedFiles([]FileChange{{Rel: "a.go", Type: FileChanged}, {Rel: "b.go", Type: FileDeleted}}); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-got:
		var p struct {
			Changes []struct {
				URI  string
				Type int
			}
		}
		_ = json.Unmarshal(m.Params, &p)
		if len(p.Changes) != 2 || p.Changes[0].Type != 2 || p.Changes[1].Type != 3 ||
			p.Changes[0].URI != fileURI(filepath.Join(root, "a.go")) {
			t.Errorf("changes = %+v", p.Changes)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no notification")
	}
}

func TestShutdownWithoutProcess(t *testing.T) {
	c, srv := newPair(t, t.TempDir())
	srv.serve(func(m inMsg) (any, bool) {
		if m.Method == "shutdown" {
			return nil, true
		}
		return nil, false
	})
	if err := c.Shutdown(ctxT(t)); err != nil {
		t.Fatal(err)
	}
}
