package imports

import (
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	"github.com/nonchan7720/depgraph/pkg/internal/safefile"
)

// PythonStdlib is the standard library of Python versions as typeshed
// records it (stdlib/VERSIONS: module -> first and last version with it),
// read from the copies the project's type checkers carry. depgraph keeps
// no list of its own: the type checker the project installs is the
// maintained source.
type PythonStdlib struct {
	copies []typeshedCopy
}

type typeshedCopy struct {
	rel  string // the VERSIONS file, repo-relative
	mods map[string]versionRange
}

type versionRange struct{ from, to [2]int } // to {0,0}: still there

// typeshedCopies are where type checkers installed with npm keep typeshed.
var typeshedCopies = []string{
	"node_modules/pyright/dist/typeshed-fallback/stdlib/VERSIONS",
	"node_modules/basedpyright/dist/typeshed-fallback/stdlib/VERSIONS",
}

// typeshedMax caps a VERSIONS file.
const typeshedMax = 1 << 20

// LoadPythonStdlib reads the typeshed copy s names (Stdlib, relative to its
// directory), else the copies in node_modules from s's directory up to the
// root; it fails when none is found.
func LoadPythonStdlib(root string, s rules.LanguageSetting) (*PythonStdlib, error) {
	var rels []string
	if s.Stdlib != "" {
		rels = []string{path.Join(s.PathFrom(s.Stdlib), "VERSIONS")}
	} else {
		for d := s.Dir; ; d = path.Dir(d) {
			for _, c := range typeshedCopies {
				rels = append(rels, path.Join(d, c))
			}
			if d == "." || d == "/" || d == "" {
				break
			}
		}
	}
	p := &PythonStdlib{}
	for _, rel := range rels {
		b, _, err := safefile.Read(root, rel, typeshedMax)
		switch {
		case errors.Is(err, os.ErrNotExist) && s.Stdlib == "":
			continue
		case err != nil:
			return nil, fmt.Errorf("languages.python of %s: %w", s.Dir, err)
		}
		mods, err := parseTypeshedVersions(b)
		if err != nil {
			return nil, fmt.Errorf("languages.python of %s: %s: %w", s.Dir, rel, err)
		}
		p.copies = append(p.copies, typeshedCopy{rel: rel, mods: mods})
	}
	if len(p.copies) == 0 {
		return nil, fmt.Errorf("languages.python of %s: no typeshed stdlib found (install pyright or basedpyright with npm, or set languages.python.stdlib)", s.Dir)
	}
	return p, nil
}

var typeshedLineRe = regexp.MustCompile(`^([A-Za-z_][\w.]*):\s*(\d+)\.(\d+)-(?:(\d+)\.(\d+))?$`)

// parseTypeshedVersions parses stdlib/VERSIONS strictly.
func parseTypeshedVersions(b []byte) (map[string]versionRange, error) {
	mods := map[string]versionRange{}
	for line := range strings.SplitSeq(string(b), "\n") {
		l, _, _ := strings.Cut(line, "#")
		if l = strings.TrimSpace(l); l == "" {
			continue
		}
		m := typeshedLineRe.FindStringSubmatch(l)
		if m == nil {
			return nil, errors.New("not a typeshed VERSIONS file")
		}
		n := func(s string) int { v, _ := strconv.Atoi(s); return v }
		r := versionRange{from: [2]int{n(m[2]), n(m[3])}}
		if m[4] != "" {
			r.to = [2]int{n(m[4]), n(m[5])}
		}
		mods[m[1]] = r
	}
	if len(mods) == 0 {
		return nil, errors.New("not a typeshed VERSIONS file (empty)")
	}
	return mods, nil
}

func (r versionRange) has(v [2]int) bool {
	before := func(a, b [2]int) bool { return a[0] < b[0] || (a[0] == b[0] && a[1] < b[1]) }
	if before(v, r.from) {
		return false
	}
	return r.to == [2]int{} || !before(r.to, v)
}

// Classify an external Python import (its Target, "ext:os.path") for the
// version minor ("3.12"): the standard library when its top-level module
// is in that version's, else third-party (an external import is the
// standard library or an installed package; a broken one is the type
// checker's to report). Copies that disagree leave it unknown ("").
func (p *PythonStdlib) Classify(target, minor string) (class, basis string) {
	name := strings.TrimPrefix(target, ExternalPrefix)
	top, _, _ := strings.Cut(name, ".")
	var v [2]int
	if _, err := fmt.Sscanf(minor, "%d.%d", &v[0], &v[1]); err != nil {
		return "", "version " + minor + " is not exact"
	}
	var inStd, outStd []string
	for _, c := range p.copies {
		if r, ok := c.mods[top]; ok && r.has(v) {
			inStd = append(inStd, c.rel)
		} else {
			outStd = append(outStd, c.rel)
		}
	}
	switch {
	case len(outStd) == 0:
		return ClassStandard, fmt.Sprintf("Python %s standard library (%s)", minor, strings.Join(inStd, ", "))
	case len(inStd) == 0:
		return ClassThirdParty, fmt.Sprintf("not in the Python %s standard library (%s)", minor, strings.Join(outStd, ", "))
	}
	return "", fmt.Sprintf("the typeshed copies disagree on %s in Python %s (%s vs %s)", top, minor,
		strings.Join(inStd, ", "), strings.Join(outStd, ", "))
}
