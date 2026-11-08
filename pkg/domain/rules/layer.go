package rules

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// ExternalPrefix starts the key of a package outside the repository
// ("ext:github.com/neo4j/neo4j-go-driver/v6/neo4j", "ext:react"), and a
// layer pattern over such packages.
const ExternalPrefix = "ext:"

// PackageRef is a package as the layer rules see it.
type PackageRef struct {
	Key string
	// External: outside the repository (Key starts with ExternalPrefix).
	External bool
	// ThirdParty: an external package the project is known to get from a
	// third party (graph.ClassThirdParty); only these match a wildcard.
	ThirdParty bool
	// Python: an external package imported by Python code, whose
	// submodules are dotted (sqlalchemy.orm under sqlalchemy).
	Python bool
}

// LayerSet is the packages of one layer, compiled from its patterns:
//
//	pkg/domain/**                in-repo package keys (Glob)
//	ext:gorm.io/gorm             an external package by name, with its
//	                             subpackages (gorm.io/gorm/clause) and, in
//	                             Python, its submodules (sqlalchemy.orm of
//	                             ext:sqlalchemy); the standard library too
//	                             (ext:net/http)
//	ext:*  ext:github.com/aws/*  external packages by a pattern where * is any
//	                             run of characters (/ included) and ? one:
//	                             only packages known to be third-party
//	                             (PackageRef.ThirdParty)
//	!<pattern>                   leaves the packages of pattern out
//
// The regular expressions mean the same in Go and in the store (see Glob).
type LayerSet struct {
	// Rx / NotRx: in-repo package keys in the layer / left out of it.
	Rx, NotRx []string
	// ExtRx: external packages named exactly (standard library included);
	// PyExtRx the same for Python imports (dotted submodules too).
	ExtRx, PyExtRx []string
	// LibRx: external packages matched by a wildcard (known third-party
	// packages only).
	LibRx []string
	// ExtNotRx / PyExtNotRx: external packages left out.
	ExtNotRx, PyExtNotRx []string
}

// Contains reports whether p is in the layer.
func (s LayerSet) Contains(p PackageRef) bool {
	if !p.External {
		return !anyRx(s.NotRx, p.Key) && anyRx(s.Rx, p.Key)
	}
	exact, not := s.ExtRx, s.ExtNotRx
	if p.Python {
		exact, not = s.PyExtRx, s.PyExtNotRx
	}
	return !anyRx(not, p.Key) && (anyRx(exact, p.Key) || (p.ThirdParty && anyRx(s.LibRx, p.Key)))
}

// Params is s as a map for a store query.
func (s LayerSet) Params() map[string]any {
	list := func(xs []string) []string {
		if xs == nil {
			return []string{}
		}
		return xs
	}
	return map[string]any{"rx": list(s.Rx), "notRx": list(s.NotRx), "extRx": list(s.ExtRx), "pyExtRx": list(s.PyExtRx),
		"libRx": list(s.LibRx), "extNotRx": list(s.ExtNotRx), "pyExtNotRx": list(s.PyExtNotRx)}
}

// HasExternal reports whether the layer has external packages.
func (s LayerSet) HasExternal() bool { return len(s.ExtRx) > 0 || len(s.LibRx) > 0 }

// rxCache holds the compiled regular expressions of anyRx by source.
var rxCache sync.Map

func anyRx(rxs []string, key string) bool {
	for _, rx := range rxs {
		v, ok := rxCache.Load(rx)
		if !ok {
			v, _ = rxCache.LoadOrStore(rx, regexp.MustCompile(rx)) // compiled by compileLayerSet / CompileGlob
		}
		if v.(*regexp.Regexp).MatchString(key) {
			return true
		}
	}
	return false
}

// compileLayerSet compiles the patterns of one layer.
func compileLayerSet(patterns []string) (LayerSet, error) {
	var s LayerSet
	positive := 0
	for _, raw := range patterns {
		p, neg := strings.CutPrefix(raw, "!")
		if name, ext := strings.CutPrefix(p, ExternalPrefix); ext {
			rx, pyRx, wildcard, err := externalRegex(name)
			if err != nil {
				return LayerSet{}, fmt.Errorf("%q: %w", raw, err)
			}
			switch {
			case neg:
				s.ExtNotRx = append(s.ExtNotRx, rx)
				s.PyExtNotRx = append(s.PyExtNotRx, pyRx)
			case wildcard:
				s.LibRx = append(s.LibRx, rx)
			default:
				s.ExtRx = append(s.ExtRx, rx)
				s.PyExtRx = append(s.PyExtRx, pyRx)
			}
		} else {
			g, err := CompileGlob(p)
			if err != nil {
				return LayerSet{}, err
			}
			if neg {
				s.NotRx = append(s.NotRx, g.Regex())
			} else {
				s.Rx = append(s.Rx, g.Regex())
			}
		}
		if !neg {
			positive++
		}
	}
	switch {
	case len(patterns) == 0:
		return LayerSet{}, errors.New("no package patterns")
	case positive == 0:
		return LayerSet{}, errors.New("only ! patterns: nothing to leave out from")
	}
	return s, nil
}

// externalRegex is the regular expression of an external package pattern
// (without "ext:"), and the one for Python imports; wildcard reports
// whether it has * or ? (the two are then the same).
func externalRegex(name string) (rx, pyRx string, wildcard bool, err error) {
	if name == "" {
		return "", "", false, errors.New("empty external package pattern")
	}
	var b strings.Builder
	b.WriteString("^" + regexp.QuoteMeta(ExternalPrefix))
	for _, r := range name {
		switch r {
		case '*':
			wildcard = true
			b.WriteString(".*")
		case '?':
			wildcard = true
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	body := b.String()
	if wildcard {
		rx = body + "$"
		pyRx = rx
	} else {
		// the package and what is under it: gorm.io/gorm/clause; in
		// Python also its submodules (sqlalchemy.orm), elsewhere a dotted
		// name is another package (lodash.debounce)
		rx = body + "(?:/.*)?$"
		pyRx = body + "(?:[/.].*)?$"
	}
	if _, err := regexp.Compile(pyRx); err != nil {
		return "", "", false, err
	}
	return rx, pyRx, wildcard, nil
}

// scopeLayerPattern makes a layer pattern of the component in dir relative
// to the root: package globs are prefixed, external names are not.
func scopeLayerPattern(dir, p string) string {
	body, neg := strings.CutPrefix(p, "!")
	if !strings.HasPrefix(body, ExternalPrefix) {
		body = PrefixGlob(dir, body)
	}
	if neg {
		return "!" + body
	}
	return body
}

// RuleExternalUnclassified: imports a wildcard external pattern would match
// if they were known to be third-party, but whose class could not be decided
// from the project's files. They are not reported as layer violations (that
// could be a false positive); this lists them, so that nothing is dropped
// silently.
const RuleExternalUnclassified = "external-unclassified"

func init() {
	builtin = append(builtin, Rule{Name: RuleExternalUnclassified, Severity: SeverityInfo,
		Description: "external imports an ext:* layer pattern could not classify (third-party or not), listed instead of dropped"})
}

// HasWildcard reports whether the layer has a wildcard external pattern.
func (s LayerSet) HasWildcard() bool { return len(s.LibRx) > 0 }
