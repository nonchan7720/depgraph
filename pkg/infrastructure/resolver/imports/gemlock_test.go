package imports

import (
	"strings"
	"testing"
)

const lockfile = `GEM
  remote: https://rubygems.org/
  specs:
    actionpack (7.1.3)
      rack (~> 2.2)
    activerecord (7.1.3)
    aws-sdk-s3 (1.140.0)
    json (2.7.1)
    pg (1.5.4)
    sidekiq (7.2.0)

GIT
  remote: https://github.com/acme/dry-things.git
  specs:
    dry-things (0.1.0)

PLATFORMS
  ruby

DEPENDENCIES
  rails

RUBY VERSION
   ruby 3.3.0p0
`

func TestRubyLockClassify(t *testing.T) {
	root := tree(t, map[string]string{"Gemfile.lock": lockfile, "app/x.rb": ""})
	lock, err := LoadGemLock(root, "app/x.rb")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		key, class string
		maybe      bool
	}{
		{"ext:ActiveRecord", ClassThirdParty, false},
		{"ext:Sidekiq", ClassThirdParty, false},
		{"ext:PG", ClassThirdParty, false},
		{"ext:Aws", ClassThirdParty, false},     // aws-sdk-s3
		{"ext:Dry", ClassThirdParty, false},     // dry-things
		{"ext:JSON", ClassThirdParty, false},    // the bundle locks the json gem
		{"ext:sidekiq", ClassThirdParty, false}, // require "sidekiq"
		{"ext:ActionController", "", true},      // probably actionpack: not confirmed
		{"ext:Hash", "", false},
		{"ext:rack", "", false}, // a dependency line of a spec is not a locked spec
	} {
		class, basis := lock.Classify(tc.key)
		if class != tc.class || (tc.class != "" && !strings.Contains(basis, "Gemfile.lock")) {
			t.Errorf("%s: %q %q, want %q", tc.key, class, basis, tc.class)
		}
		if got := lock.Maybe(tc.key) != ""; class == "" && got != tc.maybe {
			t.Errorf("%s: maybe %v, want %v", tc.key, got, tc.maybe)
		}
	}
	if _, err := LoadGemLock(tree(t, map[string]string{"a.rb": ""}), "a.rb"); err == nil {
		t.Error("no Gemfile.lock is an error")
	}
	// the nearest lock of the file
	root = tree(t, map[string]string{"Gemfile.lock": lockfile, "engines/pay/Gemfile.lock": "GEM\n  specs:\n    stripe (10.0.0)\n", "engines/pay/lib/p.rb": ""})
	lock, _ = LoadGemLock(root, "engines/pay/lib/p.rb")
	if c, _ := lock.Classify("ext:Stripe"); c != ClassThirdParty {
		t.Errorf("nearest lock: %q", c)
	}
	if c, _ := lock.Classify("ext:Sidekiq"); c != "" {
		t.Errorf("another bundle's gem: %q", c)
	}
}
