package imports

import (
	"strings"
	"testing"
)

// A Rails bundle: rails and sidekiq are what the Gemfile asks for; the
// frameworks under rails and Ruby's default gems are locked transitively.
const lockfile = `GEM
  remote: https://rubygems.org/
  specs:
    actionpack (7.1.3)
      rack (~> 2.2)
    activerecord (7.1.3)
    aws-sdk-s3 (1.140.0)
    bigdecimal (3.1.6)
    date (3.3.4)
    io-console (0.7.2)
    json (2.7.1)
    net-http (0.4.1)
    net-imap (0.4.9)
    rails (7.1.3)
    sidekiq (7.2.0)
    stringio (3.1.0)
    timeout (0.4.1)

GIT
  remote: https://github.com/acme/dry-things.git
  specs:
    dry-things (0.1.0)

PLATFORMS
  ruby

DEPENDENCIES
  aws-sdk-s3
  dry-things!
  rails (~> 7.1)
  sidekiq (>= 7)

RUBY VERSION
   ruby 3.3.0p0
`

func TestRubyLockClassify(t *testing.T) {
	root := tree(t, map[string]string{"Gemfile.lock": lockfile, "app/x.rb": ""})
	locks := NewGemLocks(root)
	lock, err := locks.For("app/x.rb")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		key, class, listed string // listed: a substring of Unclassified's reason, "" for not listed
	}{
		// what the Gemfile asks for
		{"ext:Rails", ClassThirdParty, ""},
		{"ext:Sidekiq", ClassThirdParty, ""},
		{"ext:sidekiq", ClassThirdParty, ""},     // require "sidekiq"
		{"ext:sidekiq/api", ClassThirdParty, ""}, // require "sidekiq/api"
		// locked only because another gem needs it: listed, not reported
		{"ext:ActiveRecord", "", "gem activerecord is locked but not in the Gemfile's DEPENDENCIES"},
		{"ext:Date", "", "gem date is locked but not in the Gemfile"},
		{"ext:JSON", "", "gem json is locked but not in the Gemfile"},
		{"ext:net/http", "", "gem net-http is locked but not in the Gemfile"},
		// Ruby's own: neither
		{"ext:IO", "", ""},  // not io-console
		{"ext:Net", "", ""}, // not net-imap
		{"ext:Hash", "", ""},
		{"ext:String", "", ""}, // a prefix of stringio is not a match
		{"ext:Time", "", ""},   // nor a short shared prefix with timeout
		// probably a gem's, not by its name
		{"ext:ActionController", "", "probably gem actionpack"},
		{"ext:Aws", "", "probably gem aws-sdk-s3"},
		{"ext:Dry", "", "probably gem dry-things"},
	} {
		class, basis := lock.Classify(tc.key)
		if class != tc.class || (class != "" && !strings.Contains(basis, "Gemfile.lock")) {
			t.Errorf("%s: %q %q, want %q", tc.key, class, basis, tc.class)
			continue
		}
		if class != "" {
			continue
		}
		reason := lock.Unclassified(tc.key)
		if (tc.listed == "") != (reason == "") || !strings.Contains(reason, tc.listed) {
			t.Errorf("%s: unclassified %q, want %q", tc.key, reason, tc.listed)
		}
	}
	if again, _ := locks.For("app/y.rb"); again != lock {
		t.Error("the lockfile is read again")
	}
	if _, err := NewGemLocks(tree(t, map[string]string{"a.rb": ""})).For("a.rb"); err == nil {
		t.Error("no Gemfile.lock is an error")
	}
	// the nearest lock of the file
	root = tree(t, map[string]string{"Gemfile.lock": lockfile,
		"engines/pay/Gemfile.lock": "GEM\n  specs:\n    stripe (10.0.0)\n\nDEPENDENCIES\n  stripe\n", "engines/pay/lib/p.rb": ""})
	lock, _ = NewGemLocks(root).For("engines/pay/lib/p.rb")
	if c, _ := lock.Classify("ext:Stripe"); c != ClassThirdParty {
		t.Errorf("nearest lock: %q", c)
	}
	if c, _ := lock.Classify("ext:Sidekiq"); c != "" {
		t.Errorf("another bundle's gem: %q", c)
	}
}
