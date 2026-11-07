package imports

import (
	"testing"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
)

func TestIsStdlib(t *testing.T) {
	for _, tc := range []struct {
		lang   graph.Language
		target string
		want   bool
	}{
		{graph.Go, "ext:fmt", true},
		{graph.Go, "ext:net/http", true},
		{graph.Go, "ext:C", true},
		{graph.Go, "ext:github.com/neo4j/neo4j-go-driver/v6/neo4j", false},
		{graph.Go, "ext:gopkg.in/yaml.v3", false},
		{graph.TypeScript, "ext:node:fs", true},
		{graph.TypeScript, "ext:fs", true},
		{graph.JavaScript, "ext:path", true},
		{graph.TypeScript, "ext:react", false},
		{graph.TypeScript, "ext:@nestjs/common", false},
		{graph.Python, "ext:os.path", true},
		{graph.Python, "ext:json", true},
		{graph.Python, "ext:sqlalchemy.orm", false},
		{graph.Ruby, "ext:json", true},
		{graph.Ruby, "ext:net", true},
		{graph.Ruby, "ext:rails", false},
		{graph.Go, "pkg/domain", false},
	} {
		if got := IsStdlib(tc.lang, tc.target); got != tc.want {
			t.Errorf("IsStdlib(%s, %s) = %v, want %v", tc.lang, tc.target, got, tc.want)
		}
	}
}
