package neo4jprovider

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeProvider is a Provider whose availability is fixed.
type fakeProvider struct {
	name  string
	avail error
}

func (f fakeProvider) Name() string                    { return f.name }
func (f fakeProvider) Available(context.Context) error { return f.avail }
func (f fakeProvider) Provide(context.Context, StoreRequest) (Conn, error) {
	return Conn{URI: "bolt://" + f.name}, nil
}
func (f fakeProvider) Discover(context.Context, string) (Conn, bool, error) {
	return Conn{}, false, nil
}

func TestSelect(t *testing.T) {
	ext := fakeProvider{name: External, avail: errors.New("no URI configured (--neo4j / NEO4J_URI)")}
	extOK := fakeProvider{name: External}
	ctr := fakeProvider{name: Container}
	ctrDown := fakeProvider{name: Container, avail: errors.New("none of docker, podman, nerdctl answered")}
	local := Planned(Local, "not implemented yet")

	tests := []struct {
		name      string
		explicit  string
		providers []Provider
		want      string // provider name, or "" for an error
		errHas    []string
	}{
		{"external wins when a URI is configured", "", []Provider{extOK, ctr, local}, External, nil},
		{"container when no URI", "", []Provider{ext, ctr, local}, Container, nil},
		{"explicit beats the order", Container, []Provider{extOK, ctr, local}, Container, nil},
		{"explicit external without URI is still external", External, []Provider{ext, ctr, local}, "", []string{"external", "no URI"}},
		{"explicit but unavailable", Container, []Provider{extOK, ctrDown, local}, "", []string{"container", "none of docker"}},
		{"explicit planned provider", Local, []Provider{extOK, ctr, local}, "", []string{"local", "not implemented"}},
		{"unknown name", "sqlite", []Provider{extOK, ctr, local}, "", []string{`unknown Neo4j provider "sqlite"`, "external, container, local"}},
		{"nothing available names every reason", "", []Provider{ext, ctrDown, local}, "", []string{
			"no Neo4j available", "external: no URI", "container: none of docker", "local: not implemented",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Select(context.Background(), tt.explicit, tt.providers)
			if tt.want != "" {
				if err != nil || p.Name() != tt.want {
					t.Fatalf("Select = %v, %v; want %s", p, err, tt.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("Select = %s, want an error", p.Name())
			}
			if strings.Contains(err.Error(), "\n") {
				t.Errorf("error must be one line: %q", err)
			}
			for _, s := range tt.errHas {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("error %q lacks %q", err, s)
				}
			}
		})
	}
}

func TestExternal(t *testing.T) {
	ctx := context.Background()
	if err := NewExternal("", "neo4j", "pw", "neo4j").Available(ctx); err == nil {
		t.Error("external without a URI must not be available")
	}
	e := NewExternal("bolt://db:7687", "u", "pw", "graph")
	if err := e.Available(ctx); err != nil {
		t.Fatal(err)
	}
	// Provide and Discover hand out the configured server and never manage it.
	for _, policy := range []Policy{Existing, StartIfPersistent, Start} {
		c, err := e.Provide(ctx, StoreRequest{Root: "/r", Policy: policy})
		if err != nil || c.URI != "bolt://db:7687" || c.User != "u" || c.Password != "pw" || c.Database != "graph" || c.Started {
			t.Errorf("Provide(%v) = %+v, %v", policy, c, err)
		}
		if err := c.Release(ctx); err != nil {
			t.Errorf("Release: %v", err)
		}
	}
	c, ok, err := e.Discover(ctx, "/r")
	if err != nil || !ok || c.URI != "bolt://db:7687" {
		t.Errorf("Discover = %+v, %v, %v", c, ok, err)
	}
	// the default URI of earlier releases when only the provider is named
	if c, _ := NewExternal("", "neo4j", "pw", "neo4j").WithDefaultURI().Provide(ctx, StoreRequest{}); c.URI != DefaultExternalURI {
		t.Errorf("default URI = %q", c.URI)
	}
}

func TestReleaseOfZeroConnIsSafe(t *testing.T) {
	if err := (Conn{}).Release(context.Background()); err != nil {
		t.Error(err)
	}
}
