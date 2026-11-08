package rules

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
)

// LanguageSetting says which version of a language a part of the project
// runs, so that its imports can be classified (third-party or the standard
// library) without depgraph choosing among the project's files: the user
// names one version file (recommended) or a version.
type LanguageSetting struct {
	// Dir is the directory the setting applies to: "." for the root, a
	// component's directory or pattern (packages/*), or the directory of a
	// nested .depgraph.yml.
	Dir  string
	Lang string // "python"
	// VersionFile is the file that pins the version, relative to Dir
	// (.python-version, .tool-versions, mise.toml, pyproject.toml,
	// pyrightconfig.json, Dockerfile, ...). Exclusive with Version.
	VersionFile string
	// Version is the version itself ("3.12"), when no file pins it.
	Version string
	// Stdlib is the directory of a typeshed stdlib (with its VERSIONS file),
	// relative to Dir; empty: the copies of the type checkers installed
	// in node_modules are looked up.
	Stdlib string
}

// SupportedLanguages are the languages a setting may name.
var SupportedLanguages = []string{"python"}

// PathFrom is p (relative to the setting's directory) relative to the root.
func (s LanguageSetting) PathFrom(p string) string {
	if s.Dir == "" || s.Dir == "." {
		return path.Clean(p)
	}
	return path.Clean(path.Join(s.Dir, p))
}

// versionRe: an exact version, major.minor or major.minor.patch.
var versionRe = regexp.MustCompile(`^(\d+)\.(\d+)(\.\d+)?$`)

// MinorVersion is major.minor of an exact version ("3.12" of "3.12.4"):
// the standard library's modules change only with a minor version.
func MinorVersion(v string) (string, bool) {
	m := versionRe.FindStringSubmatch(v)
	if m == nil {
		return "", false
	}
	return m[1] + "." + m[2], true
}

func (s LanguageSetting) validate() error {
	where := fmt.Sprintf("languages.%s of %s", s.Lang, s.Dir)
	if !slices.Contains(SupportedLanguages, s.Lang) {
		return fmt.Errorf("%s: language %q is not supported yet (supported: %s)", where, s.Lang, strings.Join(SupportedLanguages, ", "))
	}
	switch {
	case s.VersionFile == "" && s.Version == "":
		return fmt.Errorf("%s: set versionFile or version", where)
	case s.VersionFile != "" && s.Version != "":
		return fmt.Errorf("%s: set versionFile or version, not both", where)
	}
	if s.Version != "" {
		if _, ok := MinorVersion(s.Version); !ok {
			return fmt.Errorf("%s: version %q (want an exact version such as 3.12 or 3.12.4)", where, s.Version)
		}
	}
	for key, p := range map[string]string{"versionFile": s.VersionFile, "stdlib": s.Stdlib} {
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, "/") || strings.Contains(p, `\`) || (len(p) > 1 && p[1] == ':') {
			return fmt.Errorf("%s: %s %q: want a path relative to %s", where, key, p, s.Dir)
		}
		if r := s.PathFrom(p); r == ".." || strings.HasPrefix(r, "../") {
			return fmt.Errorf("%s: %s %q is outside the repository", where, key, p)
		}
	}
	return nil
}

func validateLanguages(ls []LanguageSetting) error {
	seen := map[string]bool{}
	for _, s := range ls {
		if err := s.validate(); err != nil {
			return err
		}
		k := s.Lang + "\x00" + s.Dir
		if seen[k] {
			return fmt.Errorf("languages.%s of %s set twice", s.Lang, s.Dir)
		}
		seen[k] = true
	}
	return nil
}

// LanguageSettingFor returns the setting of lang that applies to file
// (repo-relative): the one of the deepest directory holding it. A pattern
// directory (packages/*) applies to each directory it matches, as that
// directory (Dir is the match).
func (c Config) LanguageSettingFor(lang, file string) (LanguageSetting, bool) {
	var best LanguageSetting
	bestDepth := -1
	for _, s := range c.Languages {
		if s.Lang != lang {
			continue
		}
		dir, ok := holds(s.Dir, file)
		if !ok {
			continue
		}
		if d := depth(dir); d > bestDepth {
			best, bestDepth = s, d
			best.Dir = dir
		}
	}
	return best, bestDepth >= 0
}

// holds reports whether directory (or pattern) dir holds file, and the
// directory it stands for.
func holds(dir, file string) (string, bool) {
	if dir == "." || dir == "" {
		return ".", true
	}
	segs := strings.Split(file, "/")
	n := strings.Count(dir, "/") + 1
	if len(segs) <= n { // file directly in dir's parent, or dir is longer
		return "", false
	}
	prefix := strings.Join(segs[:n], "/")
	if !isPattern(dir) {
		return prefix, prefix == dir
	}
	g, err := CompileGlob(dir)
	if err != nil || !g.Match(prefix) {
		return "", false
	}
	return prefix, true
}

func depth(dir string) int {
	if dir == "." {
		return 0
	}
	return strings.Count(dir, "/") + 1
}
