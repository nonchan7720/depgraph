package imports

import (
	"context"
	"testing"
)

func TestRubyRequire(t *testing.T) {
	root := tree(t, map[string]string{
		"Gemfile":                      "source 'https://rubygems.org'\n",
		"lib/billing.rb":               "module Billing; end\n",
		"lib/billing/invoice.rb":       "module Billing; class Invoice; end; end\n",
		"app/models/user.rb":           "class User; end\n",
		"spec/spec_helper.rb":          "",
		"spec/models/user_spec.rb":     "",
		"test/test_helper.rb":          "",
		"test/models/user_test.rb":     "",
		"gems/money/money.gemspec":     "",
		"gems/money/lib/money.rb":      "module Money; end\n",
		"gems/money/lib/money/rate.rb": "",
		"script/run.rb":                "",
	})
	runCases(t, NewRuby(), root, []resolveCase{
		{"require lib", "app/models/user.rb", "billing", inRepo("lib")},
		{"require lib nested", "app/models/user.rb", "billing/invoice", inRepo("lib/billing")},
		{"require with ext", "app/models/user.rb", "billing.rb", inRepo("lib")},
		{"spec helper", "spec/models/user_spec.rb", "spec_helper", inRepo("spec")},
		{"test helper", "test/models/user_test.rb", "test_helper", inRepo("test")},
		{"in-repo gem", "app/models/user.rb", "money/rate", inRepo("gems/money/lib/money")},
		{"gem", "app/models/user.rb", "rails/all", ext("rails")},
		{"stdlib", "app/models/user.rb", "json", ext("json")},
		{"relative", "script/run.rb", "./../lib/billing", inRepo("lib")},
		{"relative sibling", "lib/billing.rb", "./billing/invoice", inRepo("lib/billing")},
		{"relative missing", "lib/billing.rb", "./nope", unresolved()},
		// ".." inside a spec never leaves the repository
		{"escaping spec", "app/models/user.rb", "billing/../../../outside", ext("billing")},
	})
}

// Constants resolve the way Zeitwerk loads them: every directory under app/
// (and its concerns/) of the application, its engines and its packwerk packs
// is a root, lib/ is one too, and Admin::UsersController is
// admin/users_controller.rb under one of them.
func TestRubyConstants(t *testing.T) {
	root := tree(t, map[string]string{
		"Gemfile":                                         "",
		"config/application.rb":                           "",
		"app/models/user.rb":                              "",
		"app/models/admin/role.rb":                        "",
		"app/models/concerns/taggable.rb":                 "",
		"app/models/concerns/user/searchable.rb":          "",
		"app/controllers/admin/users_controller.rb":       "",
		"app/controllers/concerns/authentication.rb":      "",
		"app/services/html_parser.rb":                     "",
		"app/services/oauth.rb":                           "",
		"lib/billing/invoice.rb":                          "",
		"package.yml":                                     "enforce_dependencies: true\n",
		"packs/payments/package.yml":                      "enforce_dependencies: true\n",
		"packs/payments/app/models/payment.rb":            "",
		"packs/payments/app/public/payments/api.rb":       "",
		"packs/payments/app/services/payments/charger.rb": "",
		"engines/blog/blog.gemspec":                       "",
		"engines/blog/app/models/blog/post.rb":            "",
		"app/assets/config/manifest.rb":                   "",
	})
	r := NewRuby()
	cases := []struct{ from, spec, want string }{
		{"app/controllers/admin/users_controller.rb", "::User", "app/models/user.rb"},
		{"app/models/user.rb", "::Admin::Role", "app/models/admin/role.rb"},
		{"app/models/user.rb", "::Admin", "app/models/admin"}, // implicit namespace
		{"app/models/user.rb", "::Taggable", "app/models/concerns/taggable.rb"},
		{"app/models/user.rb", "::User::Searchable", "app/models/concerns/user/searchable.rb"},
		{"app/controllers/admin/users_controller.rb", "::Authentication", "app/controllers/concerns/authentication.rb"},
		{"app/models/user.rb", "::HTMLParser", "app/services/html_parser.rb"},
		{"app/models/user.rb", "::OAuth", "app/services/oauth.rb"}, // acronym inflection
		{"app/models/user.rb", "::Billing::Invoice", "lib/billing/invoice.rb"},
		{"app/models/user.rb", "::Payment", "packs/payments/app/models/payment.rb"},
		{"app/models/user.rb", "::Payments::Api", "packs/payments/app/public/payments/api.rb"},
		{"packs/payments/app/models/payment.rb", "::Payments::Charger", "packs/payments/app/services/payments/charger.rb"},
		{"app/models/user.rb", "::Blog::Post", "engines/blog/app/models/blog/post.rb"},
		{"app/models/user.rb", "::ActiveRecord::Base", ""},
		{"app/models/user.rb", "::Config::Manifest", ""}, // app/assets is not autoloaded
	}
	for _, c := range cases {
		got := r.Resolve(context.Background(), root, c.from, c.spec)
		if c.want == "" {
			if got.Kind == InRepo {
				t.Errorf("%s from %s = %s, want not in the repository", c.spec, c.from, got.File)
			}
			continue
		}
		if got.Kind != InRepo || got.File != c.want {
			t.Errorf("%s from %s = %s %q, want %q", c.spec, c.from, got.Kind, got.File, c.want)
		}
	}
	// a file created after the index was built is found once it is noted
	writeFiles(t, root, map[string]string{"app/models/order.rb": ""})
	r.Note(root, "app/models/order.rb")
	if got := r.Resolve(context.Background(), root, "app/models/user.rb", "::Order"); got.File != "app/models/order.rb" {
		t.Errorf("::Order after Note = %s %q", got.Kind, got.File)
	}
	// ... also in a directory of app/ that did not exist then (a new root)
	writeFiles(t, root, map[string]string{"app/forms/signup_form.rb": ""})
	r.Note(root, "app/forms/signup_form.rb")
	if got := r.Resolve(context.Background(), root, "app/models/user.rb", "::SignupForm"); got.File != "app/forms/signup_form.rb" {
		t.Errorf("::SignupForm after Note = %s %q", got.Kind, got.File)
	}
}

func TestRubyUnderscore(t *testing.T) {
	for in, want := range map[string]string{
		"User": "user", "UsersController": "users_controller", "HTMLParser": "html_parser",
		"V1": "v1", "OAuth2Client": "o_auth2_client", "APIKey": "api_key", "Api": "api",
	} {
		if got := rubyUnderscore(in); got != want {
			t.Errorf("rubyUnderscore(%q) = %q, want %q", in, got, want)
		}
	}
}
