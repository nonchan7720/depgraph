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
	// Std: an external package of the language's standard library.
	Std bool
}

// LayerSet is the packages of one layer, compiled from its patterns:
//
//	pkg/domain/**                in-repo package keys (Glob)
//	ext:gorm.io/gorm             an external package by name, with its
//	                             subpackages / submodules (gorm.io/gorm/clause,
//	                             sqlalchemy.orm of ext:sqlalchemy); the
//	                             standard library too (ext:net/http)
//	ext:*  ext:github.com/aws/*  external packages by a pattern where * is any
//	                             run of characters (/ included) and ? one:
//	                             third-party packages only, never the standard
//	                             library
//	!<pattern>                   leaves the packages of pattern out
//
// The regular expressions mean the same in Go and in the store (see Glob).
type LayerSet struct {
	// Rx: in-repo package keys.
	Rx []string
	// ExtRx: external packages named exactly (standard library included).
	ExtRx []string
	// LibRx: external packages matched by a wildcard (third-party only).
	LibRx []string
	// NotRx: packages left out (in-repo or external).
	NotRx []string
}

// Contains reports whether p is in the layer.
func (s LayerSet) Contains(p PackageRef) bool {
	if anyRx(s.NotRx, p.Key) {
		return false
	}
	if !p.External {
		return anyRx(s.Rx, p.Key)
	}
	return anyRx(s.ExtRx, p.Key) || (!p.Std && anyRx(s.LibRx, p.Key))
}

// Params is s as a map for a store query.
func (s LayerSet) Params() map[string]any {
	list := func(xs []string) []string {
		if xs == nil {
			return []string{}
		}
		return xs
	}
	return map[string]any{"rx": list(s.Rx), "extRx": list(s.ExtRx), "libRx": list(s.LibRx), "notRx": list(s.NotRx)}
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
			rx, wildcard, err := externalRegex(name)
			if err != nil {
				return LayerSet{}, fmt.Errorf("%q: %w", raw, err)
			}
			switch {
			case neg:
				s.NotRx = append(s.NotRx, rx)
			case wildcard:
				s.LibRx = append(s.LibRx, rx)
			default:
				s.ExtRx = append(s.ExtRx, rx)
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
// (without "ext:"); wildcard reports whether it has * or ?.
func externalRegex(name string) (rx string, wildcard bool, err error) {
	if name == "" {
		return "", false, errors.New("empty external package pattern")
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
	if !wildcard {
		// the package and what is under it: gorm.io/gorm/clause, sqlalchemy.orm
		b.WriteString("(?:[/.].*)?")
	}
	b.WriteString("$")
	if _, err := regexp.Compile(b.String()); err != nil {
		return "", false, err
	}
	return b.String(), wildcard, nil
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
