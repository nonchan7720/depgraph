package rules

import "testing"

func TestFindingKey(t *testing.T) {
	if k := (Finding{Rule: "r", Message: "m"}).Key(); k != "r|m" {
		t.Errorf("key without ID = %q", k)
	}
	if k := (Finding{Rule: "r", Message: "m", ID: "x"}).Key(); k != "r|x" {
		t.Errorf("key with ID = %q", k)
	}
}

func TestDiff(t *testing.T) {
	a := Finding{Rule: "r", Message: "a"}
	b := Finding{Rule: "r", Message: "b"}
	c := Finding{Rule: "r", Message: "c"}
	added, removed := Diff([]Finding{a, b}, []Finding{b, c})
	if len(added) != 1 || added[0].Key() != c.Key() {
		t.Errorf("added = %+v", added)
	}
	if len(removed) != 1 || removed[0].Key() != a.Key() {
		t.Errorf("removed = %+v", removed)
	}
}

func TestDefaultConfig(t *testing.T) {
	c := DefaultConfig()
	if c.GodPackageFanIn <= 0 || len(c.ForbiddenLayers) == 0 {
		t.Errorf("DefaultConfig = %+v", c)
	}
}

// Rule evaluation and the post-edit hook build these findings the same way,
// so the same problem has the same identity everywhere.
func TestFindingConstructors(t *testing.T) {
	u := UnresolvedImport("src/a.ts", "@/x", `Could not resolve "@/x"`)
	if u.Rule != "unresolved-import" || u.Severity != "warn" || u.Key() != "unresolved-import|src/a.ts|@/x" ||
		u.Message != `src/a.ts imports "@/x", which does not resolve (Could not resolve "@/x")` {
		t.Errorf("UnresolvedImport = %+v", u)
	}
	if m := UnresolvedImport("a.py", "x", "").Message; m != `a.py imports "x", which does not resolve` {
		t.Errorf("no reason: %q", m)
	}
	c := ImportCycle([]string{"a", "b", "a"})
	if c.Rule != "import-cycle" || c.Severity != "error" || c.Message != "a -> b -> a" {
		t.Errorf("ImportCycle = %+v", c)
	}
	// the same cycle found from another package is the same finding
	if r := ImportCycle([]string{"c", "a", "b", "c"}); r.Message != "a -> b -> c -> a" || r.Key() != ImportCycle([]string{"b", "c", "a", "b"}).Key() {
		t.Errorf("rotation: %+v", r)
	}
}
