package treesitter

import (
	"reflect"
	"testing"
)

// ignoresOf returns caller -> the rules a `depgraph:ignore` comment of the
// function names.
func ignoresOf(t *testing.T, root, rel string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, fc := range usage(t, New(), root, rel).Calls {
		if len(fc.Ignore) > 0 {
			out[fc.Name] = fc.Ignore
		}
	}
	return out
}

// A `depgraph:ignore <rule>...` comment directly above a function, or
// inside it, exempts that function from the rules it names.
func TestIgnoreCommentGo(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/app\ngo 1.22\n")
	writeFile(t, root, "a/a.go", `package a

// Wire builds the application.
//
// depgraph:ignore wide-function (the composition root)
func Wire() { helper() }

func Inside() {
	// depgraph:ignore wide-function, pass-through
	helper()
}

// depgraph:ignore wide-function

func Detached() { helper() }

// depgraph:ignore
func NoRule() { helper() }

func Plain() { helper() }
func helper() {}
`)
	want := map[string][]string{
		"Wire":   {"wide-function"},
		"Inside": {"pass-through", "wide-function"},
	}
	if got := ignoresOf(t, root, "a/a.go"); !reflect.DeepEqual(got, want) {
		t.Errorf("ignores = %v, want %v", got, want)
	}
}

func TestIgnoreCommentTypeScriptAndPython(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"name": "app", "private": true}`)
	writeFile(t, root, "src/a.ts", `/**
 * Starts everything.
 * depgraph:ignore wide-function
 */
export function start() { helper(); }

// depgraph:ignore wide-function
export const boot = () => helper();

export function plain() { helper(); }
function helper() {}
`)
	writeFile(t, root, "app/a.py", `# depgraph:ignore wide-function
def start():
    helper()


def inside():
    # depgraph:ignore wide-function
    helper()


def plain():
    helper()


def helper():
    pass
`)
	if got, want := ignoresOf(t, root, "src/a.ts"), map[string][]string{"start": {"wide-function"}, "boot": {"wide-function"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("typescript ignores = %v, want %v", got, want)
	}
	if got, want := ignoresOf(t, root, "app/a.py"), map[string][]string{"start": {"wide-function"}, "inside": {"wide-function"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("python ignores = %v, want %v", got, want)
	}
}
