package lsp

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestChangesFor(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ChangesFor(root, []string{"a.go", "gone.go"})
	want := []FileChange{{Rel: "a.go", Type: FileChanged}, {Rel: "gone.go", Type: FileDeleted}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ChangesFor = %+v, want %+v", got, want)
	}
}
