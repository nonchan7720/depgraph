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

// abstractDesc describes abstract: (rules.Config.AbstractLayers).
const abstractDesc = "Layer -> layers it may use only through interfaces (imports stay allowed): holding or building an implementation of one although an interface it implements exists (concrete-dependency), or exposing its concrete types in the layer's API (type-leak), is reported. Names must be defined in layers (or be default layers)."

// layersDesc describes the patterns of a layer (rules.LayerSet).
const layersDesc = "Layer name -> package patterns: path globs (path segments; ** any number of segments); ext:<name> for an external package and what is under it (ext:gorm.io/gorm, ext:net/http: the standard library too); ext:<pattern> with * / ? for third-party packages (ext:* is every one, never the standard library); !<pattern> leaves packages out (\"!ext:github.com/google/uuid\")."

// languagesSchema is the schema of languages: (rules.LanguageSetting).
func languagesSchema(where string) jsonObj {
	setting := jsonObj{"type": "object", "additionalProperties": false,
		"description": "The version this part of the project runs, to classify its imports (standard library or third-party) for ext: wildcards. Name the file that pins it (recommended) or the version.",
		"oneOf":       []jsonObj{{"required": []string{"versionFile"}}, {"required": []string{"version"}}},
		"properties": jsonObj{
			"versionFile": jsonObj{"type": "string", "minLength": 1,
				"description": "The file that pins the version, relative to " + where + " (recommended): .python-version, .tool-versions, mise.toml, pyproject.toml ([tool.pyright] pythonVersion / [tool.mypy] python_version), pyrightconfig.json, Dockerfile. It may be a symlink."},
			"version": jsonObj{"type": "string", "pattern": `^\d+\.\d+(\.\d+)?$`,
				"description": "The version itself (3.12), when no file pins it."},
			"stdlib": jsonObj{"type": "string", "minLength": 1,
				"description": "A typeshed stdlib directory (with its VERSIONS file), relative to " + where + "; default: the copies in node_modules (pyright, basedpyright)."},
		}}
	props := jsonObj{}
	for _, l := range rules.SupportedLanguages {
		props[l] = setting
	}
	return jsonObj{"type": "object", "additionalProperties": false,
		"description": "Language -> the version it runs in " + where + ". The setting of the deepest directory holding a file applies.",
		"properties":  props}
}

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
	case "maxPackages":
		return jsonObj{"type": "integer", "minimum": 1, "default": rules.DefaultMaxCalledPackages,
			"description": "Report a function that calls functions of more than this many packages of the repository (its own not counted)."}
	case "deferred":
		return jsonObj{"type": "boolean", "default": rules.DefaultConfig().FileCycleDeferred,
			"description": "Also report the cycles that only close through an import inside a function (Python): a deferred import hides a cycle, it does not remove it."}
	case "ignore":
		if rules.IsUnusedRule(rule) {
			return stringList(unusedOptionDesc[option])
		}
		if rule == rules.RuleUnusedDependency {
			return stringList("Dependencies never reported (used in a way nothing in the repository shows). A name: exact, or a glob where * is any run of characters and ? one character (\"@types/*\"). For one language only: \"go:\", \"javascript:\", \"python:\" or \"ruby:\" before the name (\"ruby:pg\", \"python:*\"). For one manifest only: its path, as a path glob, and a colon before the name (\"web/package.json:lodash\", \"services/**/requirements.txt:*\"); both: \"ruby:engines/**/Gemfile:pg\". In the .depgraph.yml of a directory below the root, the entries apply to the manifests in and below that directory and manifest paths are relative to it.")
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
			"layers":    globMap(layersDesc + " Replaces the default layers and their forbidden pairs."),
			"forbidden": globMap("Layer -> layers it must not import. Names must be defined in layers (or be default layers)."),
			"abstract":  globMap(abstractDesc),
			"languages": languagesSchema("this file's directory"),
			"rules": jsonObj{"type": "object", "additionalProperties": false,
				"description": "Per rule: enabled, severity and the rule's options.", "properties": rs},
			"ignore": stringList("Package globs excluded from every rule (they are still scanned and shown in the UI)."),
			"components": jsonObj{"type": "object",
				"description": "Parts of a monorepo with layers of their own: directory (relative to this file) or a pattern over directories (packages/*: one component per directory, * and ? but no **) -> its layers and forbidden pairs, with package globs relative to that directory. The default layers are not applied inside a component; layers declared at the root apply everywhere. A directory's own .depgraph.yml does the same.",
				"additionalProperties": jsonObj{"type": "object", "additionalProperties": false,
					"properties": jsonObj{
						"layers":    globMap(layersDesc + " Package globs are relative to the component's directory. Replaces the default layers and their forbidden pairs."),
						"forbidden": globMap("Layer -> layers it must not import, within one component directory. Names must be defined in layers (or be default layers)."),
						"abstract":  globMap(abstractDesc + " Within one component directory."),
						"languages": languagesSchema("the component's directory"),
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
