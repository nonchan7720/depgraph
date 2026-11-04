package ui

import (
	"context"
	"testing"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	"github.com/nonchan7720/depgraph/pkg/domain/view"
)

type fakeReader struct{}

func (fakeReader) Overview(context.Context) (*view.Overview, error) { return sampleOverview(), nil }

func (fakeReader) PackageDetail(_ context.Context, key string) (*view.PackageDetail, error) {
	return &view.PackageDetail{Key: key}, nil
}

func TestGraphBeforeTheFirstEvaluation(t *testing.T) {
	g, err := New(fakeReader{}, "/repo").Graph(context.Background(), ViewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// findings are [] (not null) for the UI, and the view is folded
	if g.Root != "/repo" || g.Findings == nil || len(g.Findings) != 0 || len(g.View.Nodes) == 0 {
		t.Fatalf("graph = %+v", g)
	}
}

func TestGraphCarriesTheLatestFindings(t *testing.T) {
	s := New(fakeReader{}, "/repo")
	s.SetFindings(view.Stats{Packages: 5}, []rules.Finding{{Rule: "god-package", Data: map[string]any{"pkg": "cmd/app"}}})
	g, err := s.Graph(context.Background(), ViewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if g.Stats.Packages != 5 || len(g.Findings) != 1 || g.Updated.IsZero() {
		t.Fatalf("graph = %+v", g)
	}
	d, err := s.Package(context.Background(), "cmd/app")
	if err != nil || d.Key != "cmd/app" {
		t.Fatalf("package = %+v, %v", d, err)
	}
}
