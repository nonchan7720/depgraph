package imports

import (
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/nonchan7720/depgraph/pkg/internal/safefile"
)

// GemLock is what a Gemfile.lock says: the gems the Gemfile asks for
// (DEPENDENCIES) and every gem the bundle locks. A require or constant is
// third-party only when it is a gem the Gemfile asks for: a gem locked only
// as another gem's dependency may be one of Ruby's default gems (date,
// json, io-console), which every Rails bundle locks, and telling those
// apart would take a list of Ruby's per version.
type GemLock struct {
	rel    string            // the lockfile, repo-relative
	direct map[string]bool   // gem -> the Gemfile asks for it
	norm   map[string]string // normalized name (lower case, no - _ /) -> gem
}

var (
	// lockSpecRe: a spec line of a GEM / GIT / PATH section ("    name (1.2.3)").
	lockSpecRe = regexp.MustCompile(`^    ([A-Za-z0-9_.-]+) \(`)
	// lockDepRe: a line of DEPENDENCIES ("  name", "  name (>= 1)", "  name!").
	lockDepRe = regexp.MustCompile(`^  ([A-Za-z0-9_.-]+)!?(?: \(.*\))?$`)
)

// GemLocks reads the Gemfile.lock of each bundle once.
type GemLocks struct {
	root  string
	byRel map[string]*GemLock
	byDir map[string]string // a file's directory -> its lockfile ("" when none)
	errs  map[string]error  // a file's directory -> why its lockfile is unreadable
}

// NewGemLocks returns a reader of the lockfiles under root.
func NewGemLocks(root string) *GemLocks {
	return &GemLocks{root: root, byRel: map[string]*GemLock{}, byDir: map[string]string{}, errs: map[string]error{}}
}

// For returns the Gemfile.lock nearest to file (repo-relative), from its
// directory up to the root, read with safefile.
func (g *GemLocks) For(file string) (*GemLock, error) {
	dir := path.Dir(file)
	if err := g.errs[dir]; err != nil {
		return nil, err
	}
	if rel, ok := g.byDir[dir]; ok {
		if rel == "" {
			return nil, errors.New("no Gemfile.lock for " + file)
		}
		return g.byRel[rel], nil
	}
	for d := dir; ; d = path.Dir(d) {
		rel := path.Join(d, "Gemfile.lock")
		if l, ok := g.byRel[rel]; ok {
			g.byDir[dir] = rel
			return l, nil
		}
		b, err := safefile.ReadManifest(g.root, rel)
		switch {
		case err == nil:
			l := parseGemLock(rel, b)
			g.byRel[rel], g.byDir[dir] = l, rel
			return l, nil
		case !errors.Is(err, os.ErrNotExist):
			g.errs[dir] = err
			return nil, err
		}
		if d == "." || d == "/" || d == "" {
			g.byDir[dir] = ""
			return nil, errors.New("no Gemfile.lock for " + file)
		}
	}
}

func normGem(s string) string {
	return strings.NewReplacer("-", "", "_", "", "/", "").Replace(strings.ToLower(s))
}

func parseGemLock(rel string, b []byte) *GemLock {
	l := &GemLock{rel: rel, direct: map[string]bool{}, norm: map[string]string{}}
	section := ""
	for line := range strings.SplitSeq(string(b), "\n") {
		if line != "" && line[0] != ' ' {
			section = strings.TrimSpace(line)
			continue
		}
		if m := lockSpecRe.FindStringSubmatch(line); m != nil {
			l.norm[normGem(m[1])] = m[1]
		}
		if section == "DEPENDENCIES" {
			if m := lockDepRe.FindStringSubmatch(line); m != nil {
				l.direct[m[1]] = true
			}
		}
	}
	return l
}

// gem is the locked gem target names: a require by its path ("net/http":
// net-http; "sidekiq/api": sidekiq) or a constant by its normalized name
// (ActiveRecord: activerecord).
func (l *GemLock) gem(target string) string {
	name := strings.TrimPrefix(target, ExternalPrefix)
	for _, c := range []string{name, strings.ReplaceAll(name, "/", "-")} {
		if g, ok := l.norm[normGem(c)]; ok && normGem(g) == normGem(c) {
			return g
		}
	}
	if first, _, ok := strings.Cut(name, "/"); ok { // require "sidekiq/api"
		if g, ok := l.norm[normGem(first)]; ok {
			return g
		}
	}
	return ""
}

// Classify an external Ruby import: a required path ("ext:sidekiq") or a
// constant ("ext:ActiveRecord"). It is third-party when it names a gem the
// Gemfile asks for; otherwise unknown ("").
func (l *GemLock) Classify(target string) (class, basis string) {
	if g := l.gem(target); g != "" && l.direct[g] {
		return ClassThirdParty, fmt.Sprintf("gem %s of %s", g, l.rel)
	}
	return "", ""
}

// Unclassified says why an unknown import is worth listing ("" when it is
// not): it names a gem locked only as another gem's dependency, or it
// probably belongs to a locked gem by its name (ActionController and
// actionpack share a word of six letters or more, Aws prefixes aws-sdk-s3
// the Gemfile asks for; io-console, a dependency of another gem, does not
// make IO a gem's).
// Constants that are likely Ruby's own (IO, Hash, String) are not listed.
func (l *GemLock) Unclassified(target string) string {
	if g := l.gem(target); g != "" {
		return fmt.Sprintf("gem %s is locked only as a dependency of another gem (%s): name it in an ext: pattern to check it", g, l.rel)
	}
	name := normGem(strings.TrimPrefix(target, ExternalPrefix))
	best := ""
	for n, g := range l.norm {
		family := l.direct[g] && strings.Contains(g, "-") && strings.HasPrefix(n, name) && normGem(strings.SplitN(g, "-", 2)[0]) == name
		k := 0
		for k < len(n) && k < len(name) && n[k] == name[k] {
			k++
		}
		word := k >= 6 && !strings.HasPrefix(n, name) && !strings.HasPrefix(name, n)
		if (family || word) && (best == "" || g < best) {
			best = g
		}
	}
	if best == "" {
		return ""
	}
	return fmt.Sprintf("probably gem %s of %s, not confirmed by its name", best, l.rel)
}
