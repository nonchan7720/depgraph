// Package graph defines the language-neutral graph model that every resolver
// produces and the store persists.
//
//	(Package)-[:CONTAINS]->(File)-[:DECLARES]->(Symbol)
//	(File)-[:IMPORTS]->(Package)
//	(Package)-[:IMPORTS]->(Package)      derived from file-level imports
//	(Symbol)-[:CALLS|REFERENCES]->(Symbol)  filled in by LSP / SCIP resolvers
package graph

import (
	"path"
	"path/filepath"
	"sort"
)

// Language is a lower-case language identifier.
type Language string

const (
	Go         Language = "go"
	TypeScript Language = "typescript"
	JavaScript Language = "javascript"
	Python     Language = "python"
)

// Symbol is a top-level declaration inside a file.
// Kind is language-neutral: function, method, class, struct, interface, type, enum, variable.
type Symbol struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Exported bool   `json:"exported"`
	Line     int    `json:"line"`    // 1-based start line
	EndLine  int    `json:"endLine"` // 1-based end line
	// NameLine/Col locate the name identifier, for LSP position queries.
	// NameLine is 1-based (like Line); Col is a 0-based UTF-16 code unit
	// offset within that line (the unit LSP uses). Zero means unknown.
	NameLine int `json:"nameLine,omitempty"`
	Col      int `json:"col,omitempty"`
	// The shape of a function / method / class's code, for duplicate-code:
	// Tokens counts its syntax tokens, CloneHash identifies the token
	// sequence with identifiers and literals masked (a renamed copy has the
	// same hash) and Fingerprints are winnowed k-gram hashes (shared by a copy
	// that was changed a little). Empty for other kinds. Not part of the JSON
	// of `parse` (it is stored in the graph).
	Tokens       int     `json:"-"`
	CloneHash    string  `json:"-"`
	Fingerprints []int64 `json:"-"`
}

// Import is one import statement in a file.
// Target is the resolved package key inside the repo, or "ext:<spec>" when
// the import points outside the repository.
type Import struct {
	Spec     string `json:"spec"`
	Target   string `json:"target"` // package key, "ext:<name>", or "" when Unresolved
	External bool   `json:"external"`
	// Unresolved: the import should point into the repository (a relative
	// path, an alias, a module of the project) but nothing is there. It has
	// no Target and adds no dependency edge (the project's own compiler /
	// type checker reports it; depgraph does not).
	Unresolved bool   `json:"unresolved,omitempty"`
	Reason     string `json:"reason,omitempty"` // why it is Unresolved
}

// Reference is a resolved edge between two symbols (filled by LSP/SCIP).
type Reference struct {
	Kind     string `json:"kind"` // CALLS | REFERENCES | IMPLEMENTS | EXTENDS
	FromFile string `json:"fromFile"`
	FromName string `json:"fromName"`
	ToFile   string `json:"toFile"`
	ToName   string `json:"toName"`
}

// FileGraph is everything a resolver knows about a single file.
type FileGraph struct {
	Path     string      `json:"path"`    // repo-relative, slash-separated
	Package  string      `json:"package"` // package key (directory relative to repo root, "." for root)
	Language Language    `json:"language"`
	Lines    int         `json:"lines"`
	Symbols  []Symbol    `json:"symbols"`
	Imports  []Import    `json:"imports"`
	Refs     []Reference `json:"refs,omitempty"`
}

// Stats holds basic graph counts.
type Stats struct {
	Packages int64
	Files    int64
	Symbols  int64
	Imports  int64
	Calls    int64 // symbol-level CALLS edges (filled by LSP)
}

// PackageNode is a package as shown in the UI.
type PackageNode struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	External bool   `json:"external"`
	Files    int64  `json:"files"`
	Symbols  int64  `json:"symbols"`
	FanIn    int64  `json:"fanIn"`
	FanOut   int64  `json:"fanOut"`
}

// Edge is a package-level import edge.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Overview is the package-level graph.
type Overview struct {
	Packages []PackageNode `json:"packages"`
	Edges    []Edge        `json:"edges"`
}

// FileDetail is a file with its symbols and imports.
type FileDetail struct {
	Path     string   `json:"path"`
	Language string   `json:"language"`
	Lines    int64    `json:"lines"`
	Symbols  []Symbol `json:"symbols"`
	Imports  []string `json:"imports"`
}

// PackageDetail is one package's files, symbols and neighbors.
type PackageDetail struct {
	Key        string       `json:"key"`
	Files      []FileDetail `json:"files"`
	ImportedBy []string     `json:"importedBy"`
	Imports    []string     `json:"imports"`
}

// StalePaths returns the stored file paths that were not seen by the latest
// full scan (deleted while depgraph was not running, newly ignored, or left
// over from another --root), sorted.
func StalePaths(existing []string, seen map[string]struct{}) []string {
	var out []string
	for _, p := range existing {
		if _, ok := seen[p]; !ok {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// PackageKey is the package a repo-relative file belongs to: its directory
// relative to the repository root ("." for the root).
func PackageKey(rel string) string {
	d := path.Dir(filepath.ToSlash(rel))
	if d == "" {
		return "."
	}
	return d
}
