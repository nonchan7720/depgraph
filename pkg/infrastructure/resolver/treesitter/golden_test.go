package treesitter

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/golden from the current implementation")

const (
	corpusDir = "testdata/corpus"
	goldenDir = "testdata/golden"
)

// TestGolden parses every file of testdata/corpus and compares its FileGraph
// (symbols, positions, imports and their resolved targets) with
// testdata/golden/<path with "/" as "__">.json. Regenerate with
//
//	go test ./pkg/infrastructure/resolver/treesitter -run Golden -update
//
// The corpus is copied to a temporary directory first, dropping a trailing
// ".txt" from every file name: Go files (and go.mod) are stored as *.txt so
// that Go tooling never compiles them (some import modules that do not exist,
// one has a syntax error on purpose).
func TestGolden(t *testing.T) {
	root := copyCorpus(t)
	r := New()
	var rels []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel = filepath.ToSlash(rel); r.Supports(rel) {
			rels = append(rels, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) == 0 {
		t.Fatal("empty corpus")
	}

	want := map[string]bool{}
	for _, rel := range rels {
		name := strings.ReplaceAll(rel, "/", "__") + ".json"
		want[name] = true
		t.Run(rel, func(t *testing.T) {
			fg, err := r.ResolveFile(context.Background(), root, rel)
			if err != nil {
				t.Fatal(err)
			}
			got, err := json.MarshalIndent(fg, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, '\n')
			golden := filepath.Join(goldenDir, name)
			if *update {
				if err := os.MkdirAll(goldenDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			exp, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (regenerate with -update)", err)
			}
			if !bytes.Equal(got, exp) {
				t.Errorf("FileGraph differs from %s\n--- got\n%s--- want\n%s", golden, got, exp)
			}
		})
	}

	// every golden file belongs to a corpus file
	entries, err := os.ReadDir(goldenDir)
	if err != nil {
		t.Fatal(err)
	}
	var stale []string
	for _, e := range entries {
		if !want[e.Name()] {
			stale = append(stale, e.Name())
		}
	}
	sort.Strings(stale)
	for _, name := range stale {
		if *update {
			if err := os.Remove(filepath.Join(goldenDir, name)); err != nil {
				t.Fatal(err)
			}
			continue
		}
		t.Errorf("stale golden file %s (no corpus file; regenerate with -update)", name)
	}
}

// The parallel scan calls ResolveFile from several goroutines with one
// Resolver: the result must still match the goldens (run with -race).
func TestGoldenConcurrent(t *testing.T) {
	if *update {
		t.Skip("-update")
	}
	root := copyCorpus(t)
	r := New()
	var rels []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if rel = filepath.ToSlash(rel); r.Supports(rel) {
			rels = append(rels, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range rels {
				rel := rels[(i+g)%len(rels)] // every goroutine starts elsewhere
				fg, err := r.ResolveFile(context.Background(), root, rel)
				if err != nil {
					t.Error(err)
					return
				}
				got, _ := json.MarshalIndent(fg, "", "  ")
				exp, err := os.ReadFile(filepath.Join(goldenDir, strings.ReplaceAll(rel, "/", "__")+".json"))
				if err != nil {
					t.Error(err)
					return
				}
				if !bytes.Equal(append(got, '\n'), exp) {
					t.Errorf("%s: concurrent result differs from golden", rel)
				}
			}
		})
	}
	wg.Wait()
}

// copyCorpus copies testdata/corpus into a temporary directory, removing a
// trailing ".txt" from file names.
func copyCorpus(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(corpusDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(corpusDir, p)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, strings.TrimSuffix(rel, ".txt"))
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}
