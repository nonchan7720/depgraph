package lsp

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
)

// fakeLSP は Server の偽実装。位置 -> 返す内容を表で持つ。
type fakeLSP struct {
	root     string
	items    map[string][]CallHierarchyItem // key "rel:line:col"
	outgoing map[string][]OutgoingCall      // key item.Name
	incoming map[string][]IncomingCall
	refs     map[string][]Location
	failName string // この名前の prepare は失敗させる

	mu     sync.Mutex
	opened []string
	closed []string
}

func (f *fakeLSP) DidOpen(rel string, _ graph.Language) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened = append(f.opened, rel)
	return nil
}

func (f *fakeLSP) DidClose(rel string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = append(f.closed, rel)
	return nil
}

func key(rel string, line, col int) string {
	return rel + ":" + string(rune('0'+line)) + ":" + string(rune('0'+col))
}

func (f *fakeLSP) PrepareCallHierarchy(_ context.Context, rel string, line, col int) ([]CallHierarchyItem, error) {
	items := f.items[key(rel, line, col)]
	if len(items) > 0 && items[0].Name == f.failName {
		return nil, errors.New("boom")
	}
	return items, nil
}

func (f *fakeLSP) OutgoingCalls(_ context.Context, it CallHierarchyItem) ([]OutgoingCall, error) {
	return f.outgoing[it.Name], nil
}

func (f *fakeLSP) IncomingCalls(_ context.Context, it CallHierarchyItem) ([]IncomingCall, error) {
	return f.incoming[it.Name], nil
}

func (f *fakeLSP) References(_ context.Context, rel string, line, col int) ([]Location, error) {
	return f.refs[key(rel, line, col)], nil
}

func item(name, uri string, line int) CallHierarchyItem {
	r := Range{Start: Position{Line: line}, End: Position{Line: line}}
	return CallHierarchyItem{Name: name, URI: uri, SelectionRange: r, Range: r}
}

func loc(uri string, line int) Location {
	return Location{URI: uri, Range: Range{Start: Position{Line: line}, End: Position{Line: line}}}
}

func refStrings(refs []graph.Reference) []string {
	var out []string
	for _, r := range refs {
		out = append(out, r.Kind+" "+r.FromFile+"#"+r.FromName+" -> "+r.ToFile+"#"+r.ToName)
	}
	sort.Strings(out)
	return out
}

func TestEnrich(t *testing.T) {
	root := t.TempDir()
	u := func(rel string) string { return fileURI(filepath.Join(root, filepath.FromSlash(rel))) }

	// a.go: Run(1-3行, 名前は 2行目 col 5), T(5-6行, 名前 5行目 col 5)
	fg := &graph.FileGraph{Path: "a.go", Language: graph.Go, Symbols: []graph.Symbol{
		{Name: "Run", Kind: "function", Line: 1, EndLine: 3, NameLine: 2, Col: 5},
		{Name: "T", Kind: "struct", Line: 5, EndLine: 6, NameLine: 5, Col: 5},
		{Name: "v", Kind: "variable", Line: 8, EndLine: 8, NameLine: 8, Col: 4}, // 問い合わせ対象外
	}}
	// 他ファイルのシンボル表（行は 1 始まり）
	others := map[string][]graph.Symbol{
		"a.go": fg.Symbols,
		"b/b.go": {
			{Name: "Helper", Kind: "function", Line: 3, EndLine: 5},
			{Name: "Other", Kind: "function", Line: 7, EndLine: 9},
		},
		"c/c.go": {{Name: "Caller", Kind: "function", Line: 10, EndLine: 14}},
	}
	symbols := func(_ context.Context, rel string) ([]graph.Symbol, error) { return others[rel], nil }

	f := &fakeLSP{
		root: root,
		items: map[string][]CallHierarchyItem{
			key("a.go", 1, 5): {item("Run", u("a.go"), 1)},
		},
		outgoing: map[string][]OutgoingCall{"Run": {
			{To: item("Helper", u("b/b.go"), 3)},                               // 0 始まり 3 -> 4 行目 = Helper
			{To: item("Helper", u("b/b.go"), 3)},                               // 重複
			{To: item("Println", "file:///usr/local/go/src/fmt/print.go", 10)}, // リポジトリ外
			{To: item("ghost", u("b/b.go"), 50)},                               // 該当シンボル無し
		}},
		incoming: map[string][]IncomingCall{"Run": {
			{From: item("Caller", u("c/c.go"), 11)}, // 12 行目
		}},
		refs: map[string][]Location{
			key("a.go", 4, 5): {
				loc(u("a.go"), 4),   // 宣言自身の中 -> 捨てる
				loc(u("b/b.go"), 7), // Other が T を参照
				loc(u("a.go"), 1),   // Run が T を参照
				loc(u("b/b.go"), 7), // 重複
			},
		},
	}
	e := &Enricher{Server: f, Root: root, Symbols: symbols}
	if err := e.Enrich(context.Background(), fg); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"CALLS a.go#Run -> b/b.go#Helper",
		"CALLS c/c.go#Caller -> a.go#Run",
		"REFERENCES a.go#Run -> a.go#T",
		"REFERENCES b/b.go#Other -> a.go#T",
	}
	got := refStrings(fg.Refs)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("refs:\n got %q\nwant %q", got, want)
	}
	if len(f.opened) != 1 || f.opened[0] != "a.go" || len(f.closed) != 1 {
		t.Errorf("open/close = %v / %v", f.opened, f.closed)
	}
}

// 一部のシンボルの問い合わせが失敗しても、取れた分は返し、エラーも返す。
func TestEnrichPartialFailure(t *testing.T) {
	root := t.TempDir()
	u := fileURI(filepath.Join(root, "a.go"))
	fg := &graph.FileGraph{Path: "a.go", Language: graph.Go, Symbols: []graph.Symbol{
		{Name: "Bad", Kind: "function", Line: 1, EndLine: 2, NameLine: 1, Col: 5},
		{Name: "Good", Kind: "function", Line: 3, EndLine: 4, NameLine: 3, Col: 5},
	}}
	f := &fakeLSP{
		failName: "Bad",
		items: map[string][]CallHierarchyItem{
			key("a.go", 0, 5): {item("Bad", u, 0)},
			key("a.go", 2, 5): {item("Good", u, 2)},
		},
		outgoing: map[string][]OutgoingCall{"Good": {{To: item("Bad", u, 0)}}},
	}
	e := &Enricher{Server: f, Root: root, Symbols: func(context.Context, string) ([]graph.Symbol, error) { return fg.Symbols, nil }}
	err := e.Enrich(context.Background(), fg)
	if err == nil {
		t.Error("want error for failed symbol")
	}
	if got := refStrings(fg.Refs); len(got) != 1 || got[0] != "CALLS a.go#Good -> a.go#Bad" {
		t.Errorf("refs = %q", got)
	}
}

// 位置情報(NameLine)が無いシンボルは問い合わせず、サーバーも開かない。
func TestEnrichSkipsWithoutPosition(t *testing.T) {
	f := &fakeLSP{}
	fg := &graph.FileGraph{Path: "a.go", Language: graph.Go, Symbols: []graph.Symbol{{Name: "X", Kind: "function", Line: 1, EndLine: 1}}}
	e := &Enricher{Server: f, Root: t.TempDir(), Symbols: func(context.Context, string) ([]graph.Symbol, error) { return nil, nil }}
	if err := e.Enrich(context.Background(), fg); err != nil {
		t.Fatal(err)
	}
	if len(f.opened) != 0 || len(fg.Refs) != 0 {
		t.Errorf("opened=%v refs=%v", f.opened, fg.Refs)
	}
}

func TestURIToRel(t *testing.T) {
	root := t.TempDir()
	e := &Enricher{Root: root}
	if rel, ok := e.uriToRel(fileURI(filepath.Join(root, "x", "y.go"))); !ok || rel != "x/y.go" {
		t.Errorf("rel=%q ok=%v", rel, ok)
	}
	for _, bad := range []string{"file:///etc/passwd", "https://example.com/x.go", fileURI(filepath.Join(root, "..", "z.go"))} {
		if _, ok := e.uriToRel(bad); ok {
			t.Errorf("%s should be outside", bad)
		}
	}
	// %20 などのエスケープを戻す
	if rel, ok := e.uriToRel("file://" + filepath.ToSlash(root) + "/a%20b.go"); !ok || rel != "a b.go" {
		t.Errorf("rel=%q ok=%v", rel, ok)
	}
}

// ---- Manager -------------------------------------------------------------

type fakeSession struct {
	fakeLSP
	shutdown int
}

func (s *fakeSession) Shutdown(context.Context) error { s.shutdown++; return nil }

func TestManagerLazyAndSharedServer(t *testing.T) {
	var launches []graph.Language
	sess := &fakeSession{}
	m := &Manager{
		Root:    t.TempDir(),
		Symbols: func(context.Context, string) ([]graph.Symbol, error) { return nil, nil },
		Launch: func(_ context.Context, _ string, lang graph.Language) (Session, error) {
			launches = append(launches, lang)
			return sess, nil
		},
		Warn: func(string, ...any) {},
	}
	if len(launches) != 0 {
		t.Fatal("must not launch eagerly")
	}
	ts := &graph.FileGraph{Path: "a.ts", Language: graph.TypeScript}
	js := &graph.FileGraph{Path: "b.js", Language: graph.JavaScript}
	if err := m.Enrich(context.Background(), ts); err != nil {
		t.Fatal(err)
	}
	if err := m.Enrich(context.Background(), js); err != nil {
		t.Fatal(err)
	}
	if len(launches) != 1 {
		t.Errorf("TS/JS should share one server, launches = %v", launches)
	}
	m.Close(context.Background())
	if sess.shutdown != 1 {
		t.Errorf("shutdown = %d", sess.shutdown)
	}
}

func TestManagerWarnsOnceAndDisables(t *testing.T) {
	var warns []string
	n := 0
	m := &Manager{
		Root:    t.TempDir(),
		Symbols: func(context.Context, string) ([]graph.Symbol, error) { return nil, nil },
		Launch: func(context.Context, string, graph.Language) (Session, error) {
			n++
			return nil, errors.New("not found")
		},
		Warn: func(f string, a ...any) { warns = append(warns, f) },
	}
	fg := &graph.FileGraph{Path: "a.go", Language: graph.Go}
	for range 3 {
		if err := m.Enrich(context.Background(), fg); err != nil {
			t.Fatalf("disabled server must not be an error: %v", err)
		}
	}
	if n != 1 || len(warns) != 1 {
		t.Errorf("launches=%d warns=%d", n, len(warns))
	}
	m.Close(context.Background()) // パニックしない
}

func (s *fakeSession) DidChangeWatchedFiles([]FileChange) error { return nil }
