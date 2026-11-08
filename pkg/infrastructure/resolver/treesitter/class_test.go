package treesitter

import (
	"context"
	"testing"
)

// An npm package that is not installed (a fresh clone, CI before install)
// is third-party when the file's package.json, or one above it, declares it;
// Python and Ruby imports are never classified (that would take the
// interpreter, whose version may not be the project's).
func TestImportClass(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"name": "root", "devDependencies": {"vitest": "1.0.0"}}`)
	writeFile(t, root, "web/package.json", `{"name": "web", "dependencies": {"left-pad": "1.0.0"}}`)
	writeFile(t, root, "web/src/a.ts", `import pad from "left-pad";
import { test } from "vitest";
import other from "not-declared";
import fs from "node:fs";
`)
	writeFile(t, root, "app/__init__.py", "")
	writeFile(t, root, "app/a.py", "import json\nimport requests\n")
	r := New()
	t.Cleanup(r.Close)
	fg, err := r.ResolveFile(context.Background(), root, "web/src/a.ts")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][2]string{
		"left-pad":     {"third-party", "declared in web/package.json"},
		"vitest":       {"third-party", "declared in package.json"},
		"not-declared": {"", "not installed"},
		"node:fs":      {"standard", "Node built-in (esbuild)"},
	}
	for _, im := range fg.Imports {
		if w := want[im.Spec]; im.Class != w[0] || im.Basis != w[1] {
			t.Errorf("%s: class %q basis %q, want %q %q", im.Spec, im.Class, im.Basis, w[0], w[1])
		}
	}
	py, err := r.ResolveFile(context.Background(), root, "app/a.py")
	if err != nil {
		t.Fatal(err)
	}
	for _, im := range py.Imports {
		if im.Class != "" {
			t.Errorf("python %s: class %q", im.Spec, im.Class)
		}
	}
}
