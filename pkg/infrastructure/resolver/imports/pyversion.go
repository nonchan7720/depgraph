package imports

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	"github.com/nonchan7720/depgraph/pkg/internal/safefile"
)

// PythonVersion is the version a part of the project runs, as its setting
// (rules.LanguageSetting) names it.
type PythonVersion struct {
	Minor  string // 3.12: what the standard library's modules depend on
	Full   string // as pinned (3.12.4)
	Source string // the version file, repo-relative ("" for a literal version)
}

// Basis describes where the version comes from, for findings.
func (v PythonVersion) Basis() string {
	if v.Source == "" {
		return "Python " + v.Full + " (languages.python)"
	}
	return "Python " + v.Full + " (" + v.Source + ")"
}

// versionFileMax caps a version file (a version file is a few lines).
const versionFileMax = 64 << 10

// errNoPin: the file pins no Python version (a .tool-versions without
// python, a pyproject.toml with only requires-python).
var errNoPin = errors.New("no exact python version in it")

// ReadPythonVersion reads the version s names. The file is read with
// safefile (relative path, symlinks followed, a small regular file) and
// parsed strictly; errors name the file, never its content.
func ReadPythonVersion(root string, s rules.LanguageSetting) (PythonVersion, error) {
	if s.VersionFile == "" {
		minor, ok := rules.MinorVersion(s.Version)
		if !ok {
			return PythonVersion{}, fmt.Errorf("languages.python of %s: version %q is not exact", s.Dir, s.Version)
		}
		return PythonVersion{Minor: minor, Full: s.Version}, nil
	}
	rel := s.PathFrom(s.VersionFile)
	full, err := readPythonPin(root, rel)
	if err != nil {
		return PythonVersion{}, fmt.Errorf("languages.python of %s: %w", s.Dir, err)
	}
	minor, _ := rules.MinorVersion(full)
	return PythonVersion{Minor: minor, Full: full, Source: rel}, nil
}

// readPythonPin reads the exact Python version the file rel pins.
func readPythonPin(root, rel string) (string, error) {
	kind := pinKind(rel)
	if kind == "" {
		return "", fmt.Errorf("%s: unsupported version file (want .python-version, .tool-versions, mise.toml, pyproject.toml, pyrightconfig.json, mypy.ini, setup.cfg or a Dockerfile)", rel)
	}
	b, _, err := safefile.Read(root, rel, versionFileMax)
	if err != nil {
		return "", err
	}
	versions, err := parsePythonPin(kind, b)
	if err != nil {
		return "", fmt.Errorf("%s: %w", rel, err)
	}
	return versions, nil
}

var (
	miseFileRe   = regexp.MustCompile(`^\.?mise(\.[A-Za-z0-9_-]+)?\.toml$`)
	dockerFileRe = regexp.MustCompile(`^(Dockerfile|Containerfile)(\..+)?$|\.(Dockerfile|Containerfile)$`)
)

// pinKind is the format of a version file, from its name ("" when it is
// not one depgraph reads).
func pinKind(rel string) string {
	base := path.Base(rel)
	switch {
	case base == ".python-version":
		return "python-version"
	case base == ".tool-versions":
		return "tool-versions"
	case miseFileRe.MatchString(base), rel == ".config/mise.toml" || strings.HasSuffix(rel, "/.config/mise.toml"):
		return "mise"
	case base == "pyproject.toml":
		return "pyproject"
	case base == "pyrightconfig.json":
		return "pyrightconfig"
	case base == "mypy.ini" || base == ".mypy.ini" || base == "setup.cfg":
		return "ini"
	case dockerFileRe.MatchString(base):
		return "docker"
	}
	return ""
}

var errInexact = errors.New("not an exact CPython version")

// exact checks one pinned version.
func exact(v string) (string, error) {
	if _, ok := rules.MinorVersion(v); !ok {
		return "", errInexact
	}
	return v, nil
}

// one reduces the versions a file lists to its single exact one.
func one(vs []string) (string, error) {
	switch len(vs) {
	case 0:
		return "", errNoPin
	case 1:
		return exact(vs[0])
	}
	return "", errors.New("several versions: depgraph does not choose one")
}

func parsePythonPin(kind string, b []byte) (string, error) {
	switch kind {
	case "python-version":
		var vs []string
		for line := range strings.SplitSeq(string(b), "\n") {
			if l := strings.TrimSpace(line); l != "" && !strings.HasPrefix(l, "#") {
				vs = append(vs, l)
			}
		}
		return one(vs)
	case "tool-versions":
		for line := range strings.SplitSeq(string(b), "\n") {
			l, _, _ := strings.Cut(line, "#")
			f := strings.Fields(l)
			if len(f) > 0 && f[0] == "python" {
				return one(f[1:])
			}
		}
		return "", fmt.Errorf("no python in it: %w", errNoPin)
	case "mise":
		for _, e := range tomlEntries(b) {
			if e.table != "tools" || e.key != "python" {
				continue
			}
			v := strings.TrimSpace(e.value)
			switch {
			case strings.HasPrefix(v, "["):
				return one(quoted(v))
			case strings.HasPrefix(v, "{"):
				for _, kv := range tomlEntries([]byte(splitInlineTable(strings.Trim(v, "{}")))) {
					if kv.key == "version" {
						return one(quoted(kv.value))
					}
				}
				return "", errNoPin
			}
			return one(quoted(v))
		}
		return "", fmt.Errorf("no python in [tools]: %w", errNoPin)
	case "pyproject":
		var vs []string
		for _, e := range tomlEntries(b) {
			switch e.table + "." + e.key {
			case "tool.pyright.pythonVersion", "tool.basedpyright.pythonVersion", "tool.mypy.python_version":
				vs = append(vs, quoted(e.value)...)
			}
		}
		return agree(vs)
	case "pyrightconfig":
		var c struct {
			PythonVersion string `json:"pythonVersion"`
		}
		if err := json.Unmarshal(b, &c); err != nil {
			return "", errors.New("cannot be parsed as JSON")
		}
		if c.PythonVersion == "" {
			return "", errNoPin
		}
		return exact(c.PythonVersion)
	case "ini":
		var vs []string
		section := ""
		for line := range strings.SplitSeq(string(b), "\n") {
			l := strings.TrimSpace(line)
			if strings.HasPrefix(l, "[") && strings.HasSuffix(l, "]") {
				section = strings.TrimSpace(strings.Trim(l, "[]"))
				continue
			}
			if k, v, ok := strings.Cut(l, "="); ok && section == "mypy" && strings.TrimSpace(k) == "python_version" {
				vs = append(vs, strings.TrimSpace(v))
			}
		}
		return agree(vs)
	case "docker":
		return dockerPython(b)
	}
	return "", errors.New("unsupported version file")
}

// agree: the versions several tools of one file pin must be the same.
func agree(vs []string) (string, error) {
	if len(vs) == 0 {
		return "", errNoPin
	}
	for _, v := range vs[1:] {
		if v != vs[0] {
			return "", errors.New("the type checkers' versions in it disagree")
		}
	}
	return exact(vs[0])
}

// pythonImageRe: a python image (official or under a registry / namespace)
// with a tag starting with an exact version.
var pythonImageRe = regexp.MustCompile(`^(?:[\w.-]+(?::\d+)?/)?(?:library/)?python:(\d+\.\d+(?:\.\d+)?)(?:-[\w.-]+)?(?:@sha256:[0-9a-f]+)?$`)

// dockerPython: the version of the only FROM of a Dockerfile.
func dockerPython(b []byte) (string, error) {
	var froms []string
	for line := range strings.SplitSeq(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && strings.EqualFold(f[0], "FROM") {
			img := f[1]
			if strings.HasPrefix(img, "--") && len(f) >= 3 { // FROM --platform=... image
				img = f[2]
			}
			froms = append(froms, img)
		}
	}
	switch len(froms) {
	case 0:
		return "", errNoPin
	case 1:
	default:
		return "", errors.New("several FROM (a multi-stage build): depgraph does not choose one")
	}
	m := pythonImageRe.FindStringSubmatch(froms[0])
	if m == nil {
		return "", errors.New("not an exact python image (an ARG, another image, no version tag)")
	}
	return m[1], nil
}

// pinFiles are the files that may pin the Python version of a directory.
var pinFiles = []string{".python-version", ".tool-versions", "mise.toml", ".mise.toml", ".config/mise.toml",
	"mise.local.toml", ".mise.local.toml", "pyproject.toml", "pyrightconfig.json", "mypy.ini", ".mypy.ini", "setup.cfg"}

// PythonVersionConflict reports ("" when none) a file between file's
// directory and the directory of its setting s (both included) that pins a
// Python version other than v, or a mise configuration of an environment
// that sets python: depgraph does not decide which one the code runs.
func PythonVersionConflict(root string, s rules.LanguageSetting, v PythonVersion, file string) string {
	return NewPythonPins(root).Conflict(s, v, file)
}

// PythonPins reads the version files of directories once.
type PythonPins struct {
	root string
	dirs map[string][]pin
}

type pin struct {
	rel, version string
	err          error // not errNoPin
	env          bool  // a mise configuration of an environment
}

// NewPythonPins returns a reader of the version files under root.
func NewPythonPins(root string) *PythonPins {
	return &PythonPins{root: root, dirs: map[string][]pin{}}
}

// Conflict is PythonVersionConflict with the files read once.
func (p *PythonPins) Conflict(s rules.LanguageSetting, v PythonVersion, file string) string {
	for d := path.Dir(file); ; d = path.Dir(d) {
		for _, pn := range p.pins(d) {
			switch {
			case pn.rel == v.Source:
			case pn.env:
				return fmt.Sprintf("%s sets python for an environment (MISE_ENV): name the version file in languages.python", pn.rel)
			case pn.err != nil:
				return fmt.Sprintf("%s does not pin an exact version (%v), and languages.python of %s does not settle which one applies", pn.rel, pn.err, s.Dir)
			case pn.version != v.Minor:
				return fmt.Sprintf("%s pins %s, languages.python of %s says %s: set languages.python for %s", pn.rel, pn.version, s.Dir, v.Minor, path.Dir(strings.TrimSuffix(pn.rel, "/.config/mise.toml")))
			}
		}
		if d == s.Dir || d == "." || d == "/" || d == "" {
			return ""
		}
	}
}

// pins are the version files of directory d that pin python.
func (p *PythonPins) pins(d string) []pin {
	if ps, ok := p.dirs[d]; ok {
		return ps
	}
	var ps []pin
	for _, name := range pinFiles {
		rel := path.Join(d, name)
		full, err := readPythonPin(p.root, rel)
		switch {
		case errors.Is(err, os.ErrNotExist), errors.Is(err, errNoPin):
			continue
		case err != nil:
			// the reason without the file name the message names again
			ps = append(ps, pin{rel: rel, err: errors.New(strings.TrimPrefix(err.Error(), rel+": "))})
		default:
			minor, _ := rules.MinorVersion(full)
			ps = append(ps, pin{rel: rel, version: minor})
		}
	}
	entries, _ := os.ReadDir(filepath.Join(p.root, filepath.FromSlash(d)))
	for _, e := range entries {
		name := e.Name()
		if m := miseFileRe.FindStringSubmatch(name); m != nil && m[1] != "" && m[1] != ".local" {
			rel := path.Join(d, name)
			if _, err := readPythonPin(p.root, rel); !errors.Is(err, errNoPin) && !errors.Is(err, os.ErrNotExist) {
				ps = append(ps, pin{rel: rel, env: true})
			}
		}
	}
	p.dirs[d] = ps
	return ps
}

// splitInlineTable puts each key of a TOML inline table's body on a line of
// its own: commas inside quoted strings are kept.
func splitInlineTable(body string) string {
	var b strings.Builder
	var quote rune
	for _, r := range body {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
		case r == ',':
			b.WriteByte('\n')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
