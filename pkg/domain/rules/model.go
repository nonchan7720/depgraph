// Package rules holds the architectural rule model: findings, configuration
// and the diff between two evaluations.
package rules

import "sort"

// Finding is one rule violation.
type Finding struct {
	Rule     string         `json:"rule"`
	Severity string         `json:"severity"` // error | warn | info
	Message  string         `json:"message"`
	ID       string         `json:"id,omitempty"` // stable identity across runs; defaults to Message
	Data     map[string]any `json:"data,omitempty"`
}

// Key identifies a finding across runs.
func (f Finding) Key() string {
	if f.ID != "" {
		return f.Rule + "|" + f.ID
	}
	return f.Rule + "|" + f.Message
}

// Config tunes the rules.
type Config struct {
	// ForbiddenLayers: [from, to] pairs matched by substring on package key.
	ForbiddenLayers [][2]string
	// GodPackageFanIn: packages imported by at least this many packages.
	GodPackageFanIn int
	// IgnoreDuplicateNames: symbol names excluded from the duplicate check.
	IgnoreDuplicateNames []string
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		ForbiddenLayers: [][2]string{
			{"handler", "repository"}, {"handler", "infra"}, {"handler", "db"},
			{"controller", "repository"}, {"controller", "db"},
			{"domain", "infra"}, {"domain", "handler"}, {"domain", "repository"},
			{"usecase", "handler"},
		},
		GodPackageFanIn: 10,
		IgnoreDuplicateNames: []string{
			"main", "init", "New", "String", "Error", "Close", "Run", "Validate",
			"default", "index", "setup", "teardown", "handler", "render", "App",
		},
	}
}

// Diff returns findings added and removed between prev and cur.
func Diff(prev, cur []Finding) (added, removed []Finding) {
	pm := map[string]Finding{}
	for _, f := range prev {
		pm[f.Key()] = f
	}
	cm := map[string]Finding{}
	for _, f := range cur {
		cm[f.Key()] = f
		if _, ok := pm[f.Key()]; !ok {
			added = append(added, f)
		}
	}
	for k, f := range pm {
		if _, ok := cm[k]; !ok {
			removed = append(removed, f)
		}
	}
	sort.Slice(removed, func(i, j int) bool { return removed[i].Key() < removed[j].Key() })
	return added, removed
}
