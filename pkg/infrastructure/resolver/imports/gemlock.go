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

// GemLock is the gems a Gemfile.lock locks: what Bundler loads for the
// files of its bundle. It is the only evidence depgraph uses that a Ruby
// require or constant is third-party: a list of Ruby's own libraries would
// depend on the Ruby version.
type GemLock struct {
	rel  string          // the lockfile, repo-relative
	gems map[string]bool // gem name -> locked
	// norm: normalized gem name (lower case, no - or _) and the first
	// segment of a hyphenated name (aws of aws-sdk-s3) -> a gem
	norm map[string]string
}

// lockSpecRe: a spec line of a GEM / GIT / PATH section ("    name (1.2.3)").
var lockSpecRe = regexp.MustCompile(`^    ([A-Za-z0-9_.-]+) \(`)

// LoadGemLock reads the Gemfile.lock nearest to file (repo-relative) from
// its directory up to the root, with safefile.
func LoadGemLock(root, file string) (*GemLock, error) {
	for d := path.Dir(file); ; d = path.Dir(d) {
		rel := path.Join(d, "Gemfile.lock")
		b, err := safefile.ReadManifest(root, rel)
		switch {
		case err == nil:
			return parseGemLock(rel, b), nil
		case !errors.Is(err, os.ErrNotExist):
			return nil, err
		}
		if d == "." || d == "/" || d == "" {
			return nil, errors.New("no Gemfile.lock for " + file)
		}
	}
}

func normGem(s string) string {
	return strings.NewReplacer("-", "", "_", "").Replace(strings.ToLower(s))
}

func parseGemLock(rel string, b []byte) *GemLock {
	l := &GemLock{rel: rel, gems: map[string]bool{}, norm: map[string]string{}}
	for line := range strings.SplitSeq(string(b), "\n") {
		m := lockSpecRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := m[1]
		l.gems[name] = true
		l.norm[normGem(name)] = name
		if first, _, ok := strings.Cut(name, "-"); ok {
			if _, taken := l.norm[normGem(first)]; !taken {
				l.norm[normGem(first)] = name
			}
		}
	}
	return l
}

// Classify an external Ruby import: a required name ("ext:sidekiq") or a
// constant ("ext:ActiveRecord"). It is third-party when a locked gem has
// its name (normalized: ActiveRecord / activerecord, Aws / aws-sdk-s3);
// otherwise it is unknown: Ruby's own, or a gem whose constants are named
// otherwise.
func (l *GemLock) Classify(target string) (class, basis string) {
	name := strings.TrimPrefix(target, ExternalPrefix)
	first, _, _ := strings.Cut(name, "/")
	if l.gems[first] {
		return ClassThirdParty, fmt.Sprintf("gem %s locked in %s", first, l.rel)
	}
	if g, ok := l.norm[normGem(first)]; ok {
		return ClassThirdParty, fmt.Sprintf("gem %s locked in %s", g, l.rel)
	}
	return "", ""
}

// Maybe names a locked gem an unknown constant probably comes from (its
// name shares a word prefix of at least four letters with the gem's:
// ActionController / actionpack), "" when none: such a constant is worth
// listing as unclassified, Ruby's own constants are not.
func (l *GemLock) Maybe(target string) string {
	name := normGem(strings.TrimPrefix(target, ExternalPrefix))
	best := ""
	for n, g := range l.norm {
		k := 0
		for k < len(n) && k < len(name) && n[k] == name[k] {
			k++
		}
		if k >= 4 && (best == "" || g < best) {
			best = g
		}
	}
	return best
}

// Lockfile is the lockfile's relative path.
func (l *GemLock) Lockfile() string { return l.rel }
