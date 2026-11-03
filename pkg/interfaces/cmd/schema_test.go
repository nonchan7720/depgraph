package cmd

import (
	"bytes"
	"testing"

	"github.com/nonchan7720/depgraph/pkg/infrastructure/config"
)

func TestSchemaCommandPrintsTheSchema(t *testing.T) {
	var out bytes.Buffer
	c := newRootCommand(&options{})
	c.SetOut(&out)
	c.SetErr(&bytes.Buffer{})
	c.SetArgs([]string{"schema"})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	if out.String() != string(config.Schema()) {
		t.Errorf("depgraph schema printed %d bytes, want config.Schema()", out.Len())
	}
}
