package config

import (
	"encoding/json"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// SchemaID is where the published schema is served (schema/depgraph.schema.json
// on the default branch).
const SchemaID = "https://raw.githubusercontent.com/nonchan7720/depgraph/main/schema/depgraph.schema.json"

type jsonObj = map[string]any

func stringList(desc string) jsonObj {
	o := jsonObj{"type": "array", "items": jsonObj{"type": "string"}}
	if desc != "" {
		o["description"] = desc
	}
	return o
}

const (
	layersDesc   = "Layer name -> package patterns: path globs (path segments; ** any number of segments); ext:<name> for an external package and what is under it (ext:gorm.io/gorm, ext:net/http: the standard library too); ext:<pattern> with * / ? for third-party packages (ext:* is every one, never the standard library); !<pattern> leaves packages out (\"!ext:github.com/google/uuid\")."
	abstractDesc = "Layer -> layers whose types it may hold only as interfaces / abstract classes (imports stay allowed): a struct field or constructor parameter of an implementation is a concrete-dependency. Names must be defined in layers (or be default layers)."
)

// unusedOptionDesc are the options shared by the unused-code rules.
var unusedOptionDesc = map[string]string{
	"ignore":  "Path globs (files and Go package directories) never reported, e.g. a library's public API. Shared by unused-export, unreachable-file and unreachable-package.",
	"entries": "Path globs of entry points depgraph cannot see (plugins loaded by name, files an external tool runs). Shared by unused-export, unreachable-file and unreachable-package.",
}

// optionSchema is the schema of a rule option; the options are the keys of
// ruleYAML, and their checks mirror applyRule and rules.Config.Validate.
func optionSchema(rule, option string) jsonObj {
	switch option {
	case "fanIn":
		return jsonObj{"type": "integer", "minimum": 1, "default": rules.DefaultConfig().GodPackageFanIn,
			"description": "Report a package imported by at least this many packages (and importing at least fanOut)."}
	case "fanOut":
		return jsonObj{"type": "integer", "minimum": 1, "default": rules.DefaultConfig().GodPackageFanOut,
			"description": "Report only packages that also import at least this many packages of the repository: a widely used package that depends on nothing is stable, not a god package."}
	case "minTokens":
		return jsonObj{"type": "integer", "minimum": 1, "default": rules.DefaultCloneOptions().MinTokens,
			"description": "Bodies with fewer syntax tokens are not compared."}
	case "similarity":
		return jsonObj{"type": "number", "exclusiveMinimum": 0, "maximum": 1, "default": rules.DefaultCloneOptions().MinSimilarity,
			"description": "Minimum Jaccard similarity of the bodies' fingerprints (1: the same shape)."}
	case "maxLines":
		return jsonObj{"type": "integer", "minimum": 0, "default": rules.DefaultLargePackageOptions().MaxLines,
			"description": "Report a Go package with more lines than this (test and generated files left out; 0: off). A directory of another language is a set of modules, not one namespace: only layers limits it."}
	case "layers":
		return jsonObj{"type": "object", "additionalProperties": jsonObj{"type": "integer", "minimum": 1},
			"description": "Layer name (of the root's layers) -> the limit of each of its packages, in every language: an outer layer that should stay thin. The smallest limit of a package's layers applies, before maxLines."}
	case "ignore":
		if rules.IsUnusedRule(rule) {
			return stringList(unusedOptionDesc[option])
		}
		return stringList("Symbol names never reported (replaces the defaults): an exact name, a prefix such as Test*, or a glob where * is any run of characters and ? one character.")
	case "entries":
		return stringList(unusedOptionDesc[option])
	}
	return jsonObj{}
}

func ruleSchema(r rules.Rule) jsonObj {
	props := jsonObj{
		"enabled":  jsonObj{"type": "boolean", "default": true, "description": "false turns the rule off."},
		"severity": jsonObj{"enum": []string{"error", "warn", "info"}, "default": string(r.Severity), "description": "Severity of the rule's findings."},
	}
	for _, o := range r.Options {
		props[o] = optionSchema(r.Name, o)
	}
	return jsonObj{"type": "object", "additionalProperties": false, "description": r.Description, "properties": props}
}

// Schema is the JSON Schema (draft-07) of .depgraph.yml, generated from the
// rule registry and the decoder so that it cannot drift from what Parse
// accepts. Editors use it through the yaml-language-server modeline or their
// schema settings.
func Schema() []byte {
	rs := jsonObj{}
	for _, r := range rules.Builtin() {
		rs[r.Name] = ruleSchema(r)
	}
	globMap := func(desc string) jsonObj {
		return jsonObj{"type": "object", "description": desc, "additionalProperties": stringList("")}
	}
	s := jsonObj{
		"$schema":              "http://json-schema.org/draft-07/schema#",
		"$id":                  SchemaID,
		"title":                FileName,
		"description":          "depgraph settings at the scanned root, or of a directory below it (a package of a monorepo: its layers, forbidden, components, ignore and unused-code paths are relative to that directory). Every key is optional; unknown keys, rules, options and layer names are errors.",
		"type":                 "object",
		"additionalProperties": false,
		"properties": jsonObj{
			"project": jsonObj{"type": "string", "minLength": 1,
				"description": "Project key in the database (default: the directory name of the root; @<worktree> is added in a linked git worktree)."},
			"layers":    globMap(layersDesc + " Replaces the default layers and their forbidden and abstract pairs."),
			"forbidden": globMap("Layer -> layers it must not import. Names must be defined in layers (or be default layers)."),
			"abstract":  globMap(abstractDesc),
			"rules": jsonObj{"type": "object", "additionalProperties": false,
				"description": "Per rule: enabled, severity and the rule's options.", "properties": rs},
			"ignore": stringList("Package globs excluded from every rule (they are still scanned and shown in the UI)."),
			"components": jsonObj{"type": "object",
				"description": "Parts of a monorepo with layers of their own: directory (relative to this file) or a pattern over directories (packages/*: one component per directory, * and ? but no **) -> its layers and forbidden pairs, with package globs relative to that directory. The default layers are not applied inside a component; layers declared at the root apply everywhere. A directory's own .depgraph.yml does the same.",
				"additionalProperties": jsonObj{"type": "object", "additionalProperties": false,
					"properties": jsonObj{
						"layers":    globMap(layersDesc + " Package globs are relative to the component's directory. Replaces the default layers and their forbidden and abstract pairs."),
						"forbidden": globMap("Layer -> layers it must not import, within one component directory. Names must be defined in layers (or be default layers)."),
						"abstract":  globMap(abstractDesc + " Within one component directory."),
					}}},
			"store": jsonObj{"type": "object", "additionalProperties": false,
				"description": "How Neo4j is provided. Flags and environment variables take precedence.",
				"properties": jsonObj{
					"provider":     jsonObj{"enum": []string{"external", "container"}, "description": "external: the Neo4j of --neo4j / $NEO4J_URI; container: depgraph runs one (default when no URI is set)."},
					"persist":      jsonObj{"type": "boolean", "default": false, "description": "Keep the container provider's data in a named volume across runs."},
					"containerCli": jsonObj{"type": "string", "minLength": 1, "description": "Container CLI of the container provider: docker, podman, nerdctl or a path (default: the first that answers)."},
				}},
		},
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		panic(err) // static data
	}
	return append(b, '\n')
}
