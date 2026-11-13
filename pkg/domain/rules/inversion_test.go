package rules

import (
	"strings"
	"testing"
)

func TestAbstractPairsDefaultsAndValidate(t *testing.T) {
	c := DefaultConfig()
	pairs, err := c.AbstractPairs()
	if err != nil || len(pairs) != 2 || pairs[0].From != "usecase" {
		t.Errorf("default abstract pairs = %+v, %v", pairs, err)
	}
	c.AbstractLayers = [][2]string{{"nope", "db"}}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), `abstract nope -> db: unknown layer "nope"`) {
		t.Errorf("validate: %v", err)
	}
}
