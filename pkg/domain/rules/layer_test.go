package rules

import (
	"strings"
	"testing"
)

func TestLayerSetContains(t *testing.T) {
	in := func(k string) PackageRef { return PackageRef{Key: k} }
	ext := func(name string) PackageRef { return PackageRef{Key: "ext:" + name, External: true} }
	std := func(name string) PackageRef { return PackageRef{Key: "ext:" + name, External: true, Std: true} }
	py := func(name string) PackageRef { return PackageRef{Key: "ext:" + name, External: true, Python: true} }
	for _, tc := range []struct {
		patterns []string
		pkg      PackageRef
		want     bool
	}{
		// in-repo globs never match an external package, even **
		{[]string{"**"}, in("pkg/domain"), true},
		{[]string{"**"}, ext("gorm.io/gorm"), false},
		// a wildcard external pattern: third-party packages, * across slashes
		{[]string{"ext:*"}, ext("github.com/neo4j/neo4j-go-driver/v6/neo4j"), true},
		{[]string{"ext:*"}, ext("react"), true},
		{[]string{"ext:*"}, std("fmt"), false},
		{[]string{"ext:*"}, std("node:fs"), false},
		{[]string{"ext:*"}, in("pkg/domain"), false},
		{[]string{"ext:github.com/aws/*"}, ext("github.com/aws/aws-sdk-go-v2/service/s3"), true},
		{[]string{"ext:github.com/aws/*"}, ext("github.com/awslabs/x"), false},
		{[]string{"ext:@nestjs/*"}, ext("@nestjs/common"), true},
		// an exact name: the package and its subpackages / submodules, the
		// standard library included
		{[]string{"ext:gorm.io/gorm"}, ext("gorm.io/gorm"), true},
		{[]string{"ext:gorm.io/gorm"}, ext("gorm.io/gorm/clause"), true},
		{[]string{"ext:gorm.io/gorm"}, ext("gorm.io/gormx"), false},
		// a dotted submodule only of a Python import: lodash.debounce is
		// another npm package
		{[]string{"ext:sqlalchemy"}, py("sqlalchemy.orm"), true},
		{[]string{"ext:lodash"}, ext("lodash.debounce"), false},
		{[]string{"ext:lodash"}, ext("lodash/fp"), true},
		{[]string{"ext:*", "!ext:sqlalchemy"}, py("sqlalchemy.orm"), false},
		{[]string{"ext:*", "!ext:lodash"}, ext("lodash.debounce"), true},
		{[]string{"ext:net/http"}, std("net/http"), true},
		{[]string{"ext:net/http"}, std("net/http/httptest"), true},
		{[]string{"ext:database/sql"}, std("database/sqlx"), false},
		// ! leaves packages out
		{[]string{"ext:*", "!ext:github.com/google/uuid"}, ext("github.com/google/uuid"), false},
		{[]string{"ext:*", "!ext:github.com/google/uuid"}, ext("github.com/shopspring/decimal"), true},
		{[]string{"pkg/domain/**", "!pkg/domain/legacy/**"}, in("pkg/domain/legacy/x"), false},
		{[]string{"pkg/domain/**", "!pkg/domain/legacy/**"}, in("pkg/domain/user"), true},
		// a layer may mix both
		{[]string{"pkg/infra/**", "ext:*"}, in("pkg/infra/db"), true},
		{[]string{"pkg/infra/**", "ext:*"}, ext("gorm.io/gorm"), true},
	} {
		s, err := compileLayerSet(tc.patterns)
		if err != nil {
			t.Fatalf("%v: %v", tc.patterns, err)
		}
		if got := s.Contains(tc.pkg); got != tc.want {
			t.Errorf("%v contains %+v = %v, want %v", tc.patterns, tc.pkg, got, tc.want)
		}
	}
}

func TestCompileLayerSetErrors(t *testing.T) {
	for _, tc := range []struct {
		patterns []string
		want     string
	}{
		{nil, "no package patterns"},
		{[]string{"!pkg/x/**"}, "only ! patterns"},
		{[]string{"ext:"}, "empty external package pattern"},
		{[]string{"!"}, "empty package pattern"},
		{[]string{"a**"}, "**"},
	} {
		_, err := compileLayerSet(tc.patterns)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err = %v, want %q", tc.patterns, err, tc.want)
		}
	}
}

// A component's patterns are relative to its directory; external patterns
// are names, not paths, and stay as they are.
func TestScopeLayerPattern(t *testing.T) {
	for _, tc := range []struct{ dir, in, want string }{
		{"backend", "internal/domain/**", "backend/internal/domain/**"},
		{"backend", "!internal/domain/legacy/**", "!backend/internal/domain/legacy/**"},
		{"backend", "ext:*", "ext:*"},
		{"backend", "!ext:github.com/google/uuid", "!ext:github.com/google/uuid"},
	} {
		if got := scopeLayerPattern(tc.dir, tc.in); got != tc.want {
			t.Errorf("scopeLayerPattern(%q, %q) = %q, want %q", tc.dir, tc.in, got, tc.want)
		}
	}
}

// An import into an external layer crosses the pair like any other.
func TestLayerPairReportsExternal(t *testing.T) {
	c := DefaultConfig()
	c.DefaultLayers = false
	c.Layers = []Layer{
		{Name: "domain", Packages: []string{"pkg/domain/**"}},
		{Name: "frameworks", Packages: []string{"ext:*", "!ext:github.com/google/uuid"}},
		{Name: "http", Packages: []string{"ext:net/http"}},
	}
	c.ForbiddenLayers = [][2]string{{"domain", "frameworks"}, {"domain", "http"}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	pairs, err := c.LayerPairs()
	if err != nil {
		t.Fatal(err)
	}
	reported := func(to PackageRef) []string {
		var out []string
		for _, p := range pairs {
			if p.Reports(PackageRef{Key: "pkg/domain/user"}, to) {
				out = append(out, p.To)
			}
		}
		return out
	}
	for _, tc := range []struct {
		to   PackageRef
		want string
	}{
		{PackageRef{Key: "ext:gorm.io/gorm", External: true}, "frameworks"},
		{PackageRef{Key: "ext:github.com/google/uuid", External: true}, ""},
		{PackageRef{Key: "ext:fmt", External: true, Std: true}, ""},
		{PackageRef{Key: "ext:net/http", External: true, Std: true}, "http"},
		{PackageRef{Key: "pkg/domain/order"}, ""},
	} {
		if got := strings.Join(reported(tc.to), ","); got != tc.want {
			t.Errorf("domain -> %s: %q, want %q", tc.to.Key, got, tc.want)
		}
	}
}
