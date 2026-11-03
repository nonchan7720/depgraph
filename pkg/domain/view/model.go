// Package view holds the read side of the dependency graph: the counts and
// package-level views the UI, the hook and CLAUDE.md show. The write side
// (what is extracted from source and stored) is package graph.
package view

import "github.com/nonchan7720/depgraph/pkg/domain/graph"

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
	Path     string         `json:"path"`
	Language string         `json:"language"`
	Lines    int64          `json:"lines"`
	Symbols  []graph.Symbol `json:"symbols"`
	Imports  []string       `json:"imports"`
}

// PackageDetail is one package's files, symbols and neighbors.
type PackageDetail struct {
	Key        string       `json:"key"`
	Files      []FileDetail `json:"files"`
	ImportedBy []string     `json:"importedBy"`
	Imports    []string     `json:"imports"`
}
