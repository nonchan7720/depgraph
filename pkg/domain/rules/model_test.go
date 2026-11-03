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
