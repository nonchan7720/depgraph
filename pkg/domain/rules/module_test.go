package rules

import "testing"

func TestModuleOf(t *testing.T) {
	units := []string{"lib/auth", "packages/web", "packages/web/e2e", "."}
	for key, want := range map[string]string{
		"packages/web/src/api": "packages/web",
		"packages/web/e2e/x":   "packages/web/e2e",
		"packages/webkit/src":  ".", // whole segments only
		"lib/auth":             "lib/auth",
		"pkg/handler":          ".",
		".":                    ".",
	} {
		if got := ModuleOf(units, key); got != want {
			t.Errorf("ModuleOf(%q) = %q, want %q", key, got, want)
		}
	}
}
