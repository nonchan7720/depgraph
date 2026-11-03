package treesitter

import (
	"crypto/sha256"
	"encoding/hex"
	"hash/fnv"
	"slices"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
)

// hasShape lists the symbol kinds whose code is compared by duplicate-code.
var hasShape = map[string]bool{"function": true, "method": true, "class": true}

const (
	shapeK      = 4 // tokens per k-gram
	shapeWindow = 4 // winnowing window (in k-grams)
)

// shape returns the token count, the hash of the normalised token sequence
// and the winnowed fingerprints of n's code. Comments are dropped,
// identifiers become "I" and literals "L", everything else (keywords,
// operators, punctuation) stays, so renaming does not change the shape.
func (x *extractor) shape(n *gts.Node) (int, string, []int64) {
	n = x.codeBody(n)
	var toks []string
	var walk func(*gts.Node)
	walk = func(c *gts.Node) {
		t := x.typ(c)
		switch {
		case strings.Contains(t, "comment"):
			return
		case isLiteral(t):
			toks = append(toks, "L")
			return
		case c.ChildCount() == 0:
			if c.IsNamed() && strings.Contains(t, "identifier") {
				toks = append(toks, "I")
			} else if t != "" {
				toks = append(toks, t)
			}
			return
		}
		for i := 0; i < c.ChildCount(); i++ {
			walk(c.Child(i))
		}
	}
	walk(n)
	if len(toks) == 0 {
		return 0, "", nil
	}
	sum := sha256.Sum256([]byte(strings.Join(toks, " ")))
	return len(toks), hex.EncodeToString(sum[:8]), winnow(toks)
}

// codeBody is the part of a declaration whose logic is compared: the body
// of a function / method / class, so `function f() {...}`, `const f = () =>
// {...}` and a method with the same body have the same shape. The whole node
// when there is no body.
func (x *extractor) codeBody(n *gts.Node) *gts.Node {
	for range 3 {
		if b := x.field(n, "body"); b != nil {
			return b
		}
		switch {
		case x.field(n, "value") != nil: // variable_declarator: const f = () => ...
			n = x.field(n, "value")
		case x.typ(n) == "lexical_declaration" || x.typ(n) == "variable_declaration":
			if n.NamedChildCount() == 0 {
				return n
			}
			n = n.NamedChild(0)
		default:
			return n
		}
	}
	return n
}

// isLiteral reports node types that are a literal value as a whole (their
// inner nodes, such as string_content, are not looked at).
func isLiteral(t string) bool {
	switch t {
	case "number", "integer", "float", "true", "false", "none", "null", "nil", "undefined":
		return true
	}
	return strings.HasSuffix(t, "_literal") || strings.HasPrefix(t, "string") ||
		strings.HasPrefix(t, "template_string") || t == "concatenated_string"
}

// winnow hashes every k-gram of toks and keeps the minimum of each window
// (Schleimer et al., "Winnowing: local algorithms for document
// fingerprinting"): two copies that share a long enough run of tokens share
// a fingerprint for it, wherever it sits.
func winnow(toks []string) []int64 {
	if len(toks) < shapeK {
		h := fnv.New64a()
		_, _ = h.Write([]byte(strings.Join(toks, " ")))
		return []int64{int64(h.Sum64())}
	}
	grams := make([]int64, 0, len(toks)-shapeK+1)
	for i := 0; i+shapeK <= len(toks); i++ {
		h := fnv.New64a()
		for _, t := range toks[i : i+shapeK] {
			_, _ = h.Write([]byte(t))
			_, _ = h.Write([]byte{0})
		}
		grams = append(grams, int64(h.Sum64()))
	}
	seen := map[int64]bool{}
	var out []int64
	for i := 0; i+shapeWindow <= len(grams) || (i == 0 && len(grams) > 0); i++ {
		end := min(i+shapeWindow, len(grams))
		m := slices.Min(grams[i:end])
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	slices.Sort(out)
	return out
}
