package graph

import "strings"

// Usage is what a file uses of the rest of the repository and what makes
// it an entry point. It is the input of the project-level unused-code rules
// (unused-export, unreachable-file, unreachable-package) and is kept per
// file, so that re-parsing one file keeps the whole picture correct.
type Usage struct {
	// Scope is the directory of the manifest that governs the file
	// (package.json for TS/JS, go.mod for Go, pyproject.toml / setup.py /
	// setup.cfg for Python, Gemfile / *.gemspec for Ruby; "." when there is
	// none). Entry points and
	// library detection are decided per scope.
	Scope string `json:"scope"`
	// Entry is why the file is an entry point ("" when it is not): it is
	// reachable by definition and its exports are used from outside.
	Entry string `json:"entry,omitempty"`
	// Internal: the entry is the API of a package only the repository uses
	// (a private workspace package other workspace packages depend on): the
	// file is reachable, but its exports count only where the repository
	// uses them.
	Internal bool `json:"internal,omitempty"`
	// Generated: the file says it is generated; its exports are not reported.
	Generated bool `json:"generated,omitempty"`
	// Uses are the names this file uses of other files (and, for Go, of its
	// own package).
	Uses []Use `json:"uses,omitempty"`
	// Loads are the files this file's imports load when it is loaded
	// (TypeScript / JavaScript, Python): the edges of file-cycle. A Use
	// whose Target is a file, or a Python package directory with the name
	// imported from it (its submodule, else a name of its __init__.py).
	// Left out: an import the compiler erases with the types (import type, a
	// name used only as a type, `if TYPE_CHECKING:`), one made on demand
	// (import()) and a path in a string. As is LoadDeferred for an import
	// inside a function (Python): it runs when the function does.
	Loads []Use `json:"loads,omitempty"`
	// Calls are the functions each function / method / class of the file
	// calls (Go, TypeScript / JavaScript, Python): the function-level
	// dependency graph. Only callers that call something are listed.
	Calls []FuncCalls `json:"calls,omitempty"`
	// Exports maps the names this file exports under another name to the
	// declaration they stand for ("default" -> "foo", "b" -> "a").
	Exports []ExportName `json:"exports,omitempty"`
	// Keep are declared names the file marks as used on purpose (a Python
	// __all__ entry, a decorated definition a framework registers).
	Keep []string `json:"keep,omitempty"`
	// Go: Decls are the package-level names the file declares (functions,
	// types, variables, constants; exported or not) and Idents the names it
	// uses, so that the files of a package that refer to each other can be
	// told from parts nothing connects (low-cohesion).
	Decls  []string `json:"decls,omitempty"`
	Idents []string `json:"idents,omitempty"`
	// ExtConstants (Ruby): the top-level names of the constants the file
	// refers to that the repository does not declare (ActiveRecord of
	// ActiveRecord::Base): Rails code uses its frameworks through constants.
	ExtConstants []string `json:"extConstants,omitempty"`
	// DeclRefs (TypeScript, Python): the file's top-level declarations and
	// the other ones each refers to.
	DeclRefs []DeclRef `json:"declRefs,omitempty"`
	// Mentions are source file names in the file's strings ("render.js" in
	// a Go program that bundles it): the rule matches them against the
	// project's paths, and the file uses what they name.
	Mentions []string `json:"mentions,omitempty"`
}

// Use is one name a file uses from a target.
//
// Target is repo-relative and slash-separated (but see RubyConstPrefix): a
// file (TS/JS, Python, Ruby), a directory (a Go package; a Python package,
// Name then being resolved against its modules) or a directory prefix ending
// in "/" (every file under it: a dynamic import with a computed path).
//
// Name is the name used ("default" for a default import); dotted for Python
// ("sub.f": the attribute f of submodule sub, or of the name sub);
// "*" means every export of Target, "" only the file itself (a side-effect
// import, a path in a string).
//
// A re-export (`export { a as b } from './x'`, `export * from './x'`) is a
// Use with ReExport set and As the name this file exports it under ("*" for
// `export *`): it only counts when someone uses that name of this file.
type Use struct {
	Target   string `json:"target"`
	Name     string `json:"name,omitempty"`
	ReExport bool   `json:"reexport,omitempty"`
	As       string `json:"as,omitempty"`
}

// RubyConstPrefix starts the Target of a use of a Ruby constant: the rest
// is the lexical nesting it is written in (full names, innermost first,
// space-separated; empty at the top level) and Name is the constant as
// written ("User", "Admin::Role", "::Billing"; "Importers::*" for every
// constant under Importers). It is looked up the way Ruby does, among the
// constants the project declares.
const RubyConstPrefix = "::"

// LoadDeferred marks (as Use.As) a load made by an import inside a function.
const LoadDeferred = "deferred"

// ExportName maps an exported name to the local declaration it exports.
type ExportName struct {
	Name  string `json:"name"`
	Local string `json:"local"`
}

// usesSep separates the fields of an encoded Use: the unit separator never
// occurs in a path or identifier.
const usesSep = "\x1f"

// Encode returns u as one string (for list properties of the store).
func (u Use) Encode() string {
	re := ""
	if u.ReExport {
		re = "r"
	}
	return strings.Join([]string{u.Target, u.Name, re, u.As}, usesSep)
}

// DecodeUse is the inverse of Use.Encode.
func DecodeUse(s string) (Use, bool) {
	f := strings.Split(s, usesSep)
	if len(f) != 4 {
		return Use{}, false
	}
	return Use{Target: f[0], Name: f[1], ReExport: f[2] == "r", As: f[3]}, true
}

// Encode returns e as "name=local".
func (e ExportName) Encode() string { return e.Name + "=" + e.Local }

// DecodeExportName is the inverse of ExportName.Encode.
func DecodeExportName(s string) (ExportName, bool) {
	name, local, ok := strings.Cut(s, "=")
	if !ok || name == "" || local == "" {
		return ExportName{}, false
	}
	return ExportName{Name: name, Local: local}, true
}
