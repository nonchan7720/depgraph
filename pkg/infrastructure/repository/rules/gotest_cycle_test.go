package rules

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// The import of a Go test file closes no cycle: an external test package
// (package v1_test) is another package than the one in its directory, and a
// test file is only compiled with its own package's tests. Go builds such
// code (kubernetes: pkg/apis/apps/install imports v1, v1's defaults_test.go
// imports install). A cycle of non-test files, and one through a test file
// of another language, are still cycles.
func TestImportCycleIgnoresGoTestFiles(t *testing.T) {
	ctx, st := connect(t)
	key := fmt.Sprintf("zz_test_%d_gotest", time.Now().UnixNano())
	file := func(path string, lang graph.Language, imports ...string) *graph.FileGraph {
		fg := &graph.FileGraph{Path: path, Package: graph.PackageKey(path), Language: lang}
		for _, im := range imports {
			fg.Imports = append(fg.Imports, graph.Import{Spec: im, Target: im})
		}
		return fg
	}
	upsertAll(ctx, t, st, key, []*graph.FileGraph{
		// install -> v1; only v1's test imports install back
		file("apis/install/install.go", graph.Go, "apis/v1"),
		file("apis/v1/types.go", graph.Go),
		file("apis/v1/defaults_test.go", graph.Go, "apis/install"),
		// both directions only in test files
		file("proxy/ipvs/proxier.go", graph.Go),
		file("proxy/ipvs/proxier_test.go", graph.Go, "proxy/ipvs/testing"),
		file("proxy/ipvs/testing/fake.go", graph.Go),
		file("proxy/ipvs/testing/fake_test.go", graph.Go, "proxy/ipvs"),
		// a real cycle, next to a test import of the same direction
		file("svc/a/a.go", graph.Go, "svc/b"),
		file("svc/b/b.go", graph.Go, "svc/a"),
		file("svc/b/b_test.go", graph.Go, "svc/a"),
		// TypeScript has no test packages: a test file's import is an import
		file("web/x/x.ts", graph.TypeScript, "web/y"),
		file("web/y/y.test.ts", graph.TypeScript, "web/x"),
	})
	cfg := rules.DefaultConfig()
	cfg.Modules = []string{"apis", "proxy"}
	fs, err := New(st, key).Evaluate(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range fs {
		if f.Rule == rules.RuleImportCycle || f.Rule == rules.RuleModuleCycle {
			got = append(got, f.Rule+": "+f.Message)
		}
	}
	want := []string{
		"import-cycle: svc/a -> svc/b -> svc/a",
		"import-cycle: web/x -> web/y -> web/x",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("cycles =\n %q, want\n %q", got, want)
	}
}
