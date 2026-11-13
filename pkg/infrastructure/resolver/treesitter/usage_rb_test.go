package treesitter

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// symbolSet renders symbols as "kind name" (+ " private" when not exported).
func symbolSet(fg *graph.FileGraph) []string {
	var out []string
	for _, s := range fg.Symbols {
		e := s.Kind + " " + s.Name
		if !s.Exported {
			e += " private"
		}
		out = append(out, e)
	}
	return out
}

func TestRubyDeclarations(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "Gemfile", "")
	writeFile(t, root, "app/models/admin/user.rb", `module Admin
  class User < ApplicationRecord
    include Searchable

    class Error < StandardError; end
    ROLES = %w[a b].freeze

    def self.build; end

    def name
      "x"
    end

    class << self
      def table; end
    end

    private

    def secret; end
    public def shown; end
  end
end

module Admin::Tools
  def tool; end
end

module Searchable
  extend ActiveSupport::Concern

  included do
    scope :search, -> { where(x: 1) }
  end

  class_methods do
    def search_by; end
  end
end

Point = Struct.new(:x, :y)

def helper; end
`)
	r := New()
	t.Cleanup(r.Close)
	fg, err := r.ResolveFile(context.Background(), root, "app/models/admin/user.rb")
	if err != nil {
		t.Fatal(err)
	}
	if fg.Language != graph.Ruby {
		t.Fatalf("language = %q", fg.Language)
	}
	want := []string{
		"module Admin private", // a namespace only: nothing to report
		"class Admin::User",
		"class Admin::User::Error",
		"constant Admin::User::ROLES",
		"method Admin::User.build",
		"method Admin::User#name",
		"method Admin::User.table",
		"method Admin::User#secret private",
		"method Admin::User#shown",
		"module Admin::Tools",
		"method Admin::Tools#tool",
		"module Searchable",
		"method Searchable.search_by",
		"class Point",
		"function helper",
	}
	if got := symbolSet(fg); !slices.Equal(got, want) {
		t.Errorf("symbols:\n  got  %q\n  want %q", got, want)
	}
	for _, s := range fg.Symbols {
		switch s.Kind {
		case "method", "function":
			if s.CloneHash == "" {
				t.Errorf("%s: no shape for duplicate-code", s.Name)
			}
		case "class", "module":
			if s.CloneHash != "" {
				t.Errorf("%s: a class is compared by its methods, not as a whole", s.Name)
			}
		}
	}
}

func TestUsageRuby(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "Gemfile", "")
	writeFile(t, root, "config/application.rb", "")
	for _, f := range []string{
		"app/models/user.rb", "app/models/comment.rb", "app/models/person.rb", "app/models/category.rb",
		"app/models/admin/role.rb", "app/models/concerns/taggable.rb", "app/validators/email_validator.rb",
		"app/controllers/application_controller.rb", "lib/billing.rb", "app/services/importers/csv.rb",
	} {
		writeFile(t, root, f, "")
	}
	writeFile(t, root, "app/controllers/admin/users_controller.rb", `require "billing"
require_relative "../application_controller"
require "json"

module Admin
  class UsersController < ApplicationController
    include Taggable
    has_many :comments
    has_many :people
    has_many :categories
    belongs_to :owner, class_name: "User"
    belongs_to :subject, polymorphic: true
    validates :email, email: true, presence: true

    def index
      @users = User.where(active: true)
      Role.first
      ::Billing.charge
      "Importers::#{kind.camelize}".constantize
      self.class::Thing
    end
  end
end
`)
	r := New()
	t.Cleanup(r.Close)
	fg, err := r.ResolveFile(context.Background(), root, "app/controllers/admin/users_controller.rb")
	if err != nil {
		t.Fatal(err)
	}
	u := fg.Usage
	scope := "::Admin::UsersController Admin"
	wantUses(t, u.Uses,
		"lib/billing.rb#", "app/controllers/application_controller.rb#",
		"::Admin#ApplicationController", // a superclass is looked up outside the class
		scope+"#Taggable", scope+"#User", scope+"#Role", scope+"#::Billing",
		scope+"#Comment", scope+"#Person", scope+"#Category", // associations (singularized)
		scope+"#EmailValidator", // validates ... email: true
		"::#User",               // class_name: "User"
		"::#Importers::*",       // a constant name built at run time
	)
	noUses(t, u.Uses, scope+"#Subject", scope+"#Owner", scope+"#PresenceValidator", scope+"#Thing")

	imps := map[string]string{}
	for _, im := range fg.Imports {
		imps[im.Spec] = im.Target
		if im.Unresolved {
			t.Errorf("import %q unresolved: %s", im.Spec, im.Reason)
		}
	}
	for spec, target := range map[string]string{
		"billing": "lib", "../application_controller": "app/controllers", "json": "ext:json",
		"Taggable": "app/models/concerns", "User": "app/models", "Role": "app/models/admin",
		"Comment": "app/models", "Person": "app/models", "EmailValidator": "app/validators",
	} {
		if imps[spec] != target {
			t.Errorf("import %q -> %q, want %q (all: %v)", spec, imps[spec], target, imps)
		}
	}
	if _, ok := imps["StandardError"]; ok {
		t.Errorf("a constant outside the repository is not an import: %v", imps)
	}
}

func TestUsageRubyTemplate(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "Gemfile", "")
	writeFile(t, root, "app/components/button_component.rb", "class ButtonComponent; end\n")
	writeFile(t, root, "app/views/users/index.html.erb", `<h1>Users Of Example</h1>
<%= render ButtonComponent.new(label: "Hi") %>
<% Admin::Stats.each do |s| %><% end %>
`)
	writeFile(t, root, "app/views/users/show.html.haml", "%h1 Welcome Home\n= render Card.new\n%p= Admin::Badge.for(user)\n")
	r := New()
	t.Cleanup(r.Close)
	fg, err := r.ResolveFile(context.Background(), root, "app/views/users/index.html.erb")
	if err != nil {
		t.Fatal(err)
	}
	wantUses(t, fg.Usage.Uses, "::#ButtonComponent", "::#Admin::Stats")
	noUses(t, fg.Usage.Uses, "::#Users", "::#Of", "::#Example")
	if fg.Usage.Entry == "" {
		t.Errorf("a view template is an entry")
	}
	if len(fg.Imports) != 1 || fg.Imports[0].Target != "app/components" {
		t.Errorf("imports = %+v", fg.Imports)
	}
	h := usage(t, r, root, "app/views/users/show.html.haml")
	wantUses(t, h.Uses, "::#Card", "::#Admin::Badge")
	noUses(t, h.Uses, "::#Welcome", "::#Home")
}

func TestRubyEntries(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"Gemfile":                             "",
		"config/application.rb":               "",
		"config/routes.rb":                    "",
		"config/initializers/x.rb":            "",
		"db/seeds.rb":                         "",
		"db/migrate/20240101_create_users.rb": "",
		"Rakefile":                            "",
		"lib/tasks/x.rake":                    "",
		"config.ru":                           "",
		"bin/setup":                           "",
		"app/controllers/users_controller.rb": "",
		"app/controllers/concerns/authentication.rb":    "",
		"app/jobs/sync_job.rb":                          "",
		"app/mailers/user_mailer.rb":                    "",
		"app/helpers/users_helper.rb":                   "",
		"app/channels/chat_channel.rb":                  "",
		"app/policies/user_policy.rb":                   "",
		"app/views/users/index.json.jbuilder":           "",
		"app/models/user.rb":                            "",
		"app/models/concerns/taggable.rb":               "",
		"app/services/report.rb":                        "",
		"lib/billing.rb":                                "",
		"spec/models/user_spec.rb":                      "",
		"test/models/user_test.rb":                      "",
		"script/run.rb":                                 "#!/usr/bin/env ruby\n",
		"tools/once.rb":                                 "if __FILE__ == $0\n  run\nend\n",
		"app/models/legacy.rb":                          "# depgraph:entry\n",
		"packs/pay/package.yml":                         "enforce_privacy: true\n",
		"packs/pay/app/public/pay/api.rb":               "",
		"packs/pay/app/models/pay/charge.rb":            "",
		"packs/ship/package.yml":                        "public_path: app/api\n",
		"packs/ship/app/api/ship/client.rb":             "",
		"gems/money/money.gemspec":                      "",
		"gems/money/lib/money.rb":                       "",
		"gems/money/lib/money/rate.rb":                  "",
		"engines/blog/blog.gemspec":                     "",
		"engines/blog/app/models/blog/post.rb":          "",
		"engines/blog/app/controllers/blog/posts.rb":    "",
		"engines/blog/lib/blog/engine.rb":               "",
		"app/models/admin.rb":                           "",
		"app/controllers/concerns/admin/scoped.rb":      "",
		"app/controllers/admin/dashboard_controller.rb": "",
	}
	for f, c := range files {
		writeFile(t, root, f, c)
	}
	r := New()
	t.Cleanup(r.Close)
	entry := func(rel string) *graph.Usage {
		t.Helper()
		return usage(t, r, root, rel)
	}
	for _, rel := range []string{
		"config/application.rb", "config/routes.rb", "config/initializers/x.rb", "db/seeds.rb",
		"db/migrate/20240101_create_users.rb", "Rakefile", "lib/tasks/x.rake", "config.ru",
		"app/controllers/users_controller.rb", "app/jobs/sync_job.rb", "app/mailers/user_mailer.rb",
		"app/helpers/users_helper.rb", "app/channels/chat_channel.rb", "app/policies/user_policy.rb",
		"app/views/users/index.json.jbuilder", "spec/models/user_spec.rb", "test/models/user_test.rb",
		"script/run.rb", "tools/once.rb", "app/models/legacy.rb",
		"gems/money/lib/money.rb", "gems/money/lib/money/rate.rb", "engines/blog/lib/blog/engine.rb",
		"engines/blog/app/controllers/blog/posts.rb", "app/controllers/admin/dashboard_controller.rb",
	} {
		if u := entry(rel); u.Entry == "" {
			t.Errorf("%s: not an entry", rel)
		}
	}
	for _, rel := range []string{
		"app/models/user.rb", "app/models/concerns/taggable.rb", "app/services/report.rb", "lib/billing.rb",
		"app/controllers/concerns/authentication.rb", // a concern is used by including it
		"app/controllers/concerns/admin/scoped.rb",
		"packs/pay/app/models/pay/charge.rb", "engines/blog/app/models/blog/post.rb",
	} {
		if u := entry(rel); u.Entry != "" {
			t.Errorf("%s: entry %q, want none", rel, u.Entry)
		}
	}
	// a pack's public API is reachable, but only what the repository uses of
	// it counts (packs are used by the application only)
	for _, rel := range []string{"packs/pay/app/public/pay/api.rb", "packs/ship/app/api/ship/client.rb"} {
		if u := entry(rel); u.Entry == "" || !u.Internal {
			t.Errorf("%s: entry %q internal %v, want the internal API of a pack", rel, u.Entry, u.Internal)
		}
	}
	if s := entry("packs/pay/app/models/pay/charge.rb").Scope; s != "." {
		t.Errorf("scope of a pack file = %q, want the application's", s)
	}
	if s := entry("engines/blog/app/models/blog/post.rb").Scope; s != "engines/blog" {
		t.Errorf("scope of an engine file = %q", s)
	}
}

func TestRubyKeep(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "Gemfile", "")
	writeFile(t, root, "app/models/user.rb", "class User < ApplicationRecord; end\n")
	writeFile(t, root, "app/models/admin_user.rb", "class AdminUser < User; end\n")
	writeFile(t, root, "lib/my/railtie.rb", "module My\n  class Railtie < Rails::Railtie; end\nend\n")
	r := New()
	t.Cleanup(r.Close)
	if u := usage(t, r, root, "app/models/admin_user.rb"); !slices.Contains(u.Keep, "AdminUser") || u.Entry == "" {
		t.Errorf("an STI subclass is loaded from the type column: keep = %q, entry %q", u.Keep, u.Entry)
	}
	if k := usage(t, r, root, "app/models/user.rb").Keep; slices.Contains(k, "User") {
		t.Errorf("keep = %q", k)
	}
	if u := usage(t, r, root, "lib/my/railtie.rb"); !slices.Contains(u.Keep, "My::Railtie") || u.Entry == "" {
		t.Errorf("a Railtie is registered by subclassing: keep = %q, entry %q", u.Keep, u.Entry)
	}
}

// Constant references between packwerk packs are imports between them, so
// module-cycle sees a cycle of packs (packwerk itself checks the declared
// dependencies, not cycles in what the code uses).
func TestRubyPackImports(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "Gemfile", "")
	writeFile(t, root, "package.yml", "")
	writeFile(t, root, "packs/orders/package.yml", "enforce_dependencies: true\n")
	writeFile(t, root, "packs/billing/package.yml", "enforce_dependencies: true\n")
	writeFile(t, root, "packs/orders/app/models/order.rb", "class Order < ApplicationRecord\n  def bill = Billing::Invoice.for(self)\nend\n")
	writeFile(t, root, "packs/billing/app/public/billing/invoice.rb", "module Billing\n  class Invoice\n    def order\n      Order.find(1)\n    end\n  end\nend\n")
	r := New()
	t.Cleanup(r.Close)
	var edges [][2]string
	for _, rel := range []string{"packs/orders/app/models/order.rb", "packs/billing/app/public/billing/invoice.rb"} {
		fg, err := r.ResolveFile(context.Background(), root, rel)
		if err != nil {
			t.Fatal(err)
		}
		for _, im := range fg.Imports {
			if !im.External && im.Target != "" {
				edges = append(edges, [2]string{fg.Package, im.Target})
			}
		}
	}
	// the two packs import each other: a cycle of modules (module-cycle)
	units := []string{"packs/orders", "packs/billing"}
	hops := map[[2]string]bool{}
	for _, e := range edges {
		hops[[2]string{rules.ModuleOf(units, e[0]), rules.ModuleOf(units, e[1])}] = true
	}
	if !hops[[2]string{"packs/orders", "packs/billing"}] || !hops[[2]string{"packs/billing", "packs/orders"}] {
		t.Errorf("edges %v: the packs do not import each other", edges)
	}
}

// Real-world conventions seen in Rails applications (Mastodon): what Ruby
// or a library loads without the project naming it.
func TestRubyLibraryConventions(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "Gemfile", "gem 'rails'\ngem 'kt-paperclip', '~> 8.0'\ngem \"chewy\"\n")
	writeFile(t, root, "Gemfile.lock", "GEM\n  specs:\n    activerecord (8.0.0)\n    kt-paperclip (8.0.0)\n    chewy (8.0.0)\n")
	writeFile(t, root, ".rubocop.yml", "require:\n  - ./lib/linter/cop.rb\ninherit_from:\n  - .rubocop/custom.yml\n")
	writeFile(t, root, ".rubocop/custom.yml", "require:\n  - ../lib/linter/other_cop\n")
	writeFile(t, root, ".haml-lint.yml", "require:\n  - ./lib/linter/haml_dot.rb\n")
	writeFile(t, root, ".custom_cops.yml", "require:\n  - ./lib/custom_cops/my_cop.rb\n") // standard's extend_config
	writeFile(t, root, ".active_record_doctor.rb", "ActiveRecordDoctor.configure do\nend\n")
	writeFile(t, root, "sorbet/tapioca/require.rb", "require \"rails\"\n")
	files := map[string]string{
		"lib/linter/cop.rb":         "class Cop; end\n",
		"lib/linter/other_cop.rb":   "class OtherCop; end\n",
		"lib/linter/haml_dot.rb":    "class HamlDot; end\n",
		"lib/custom_cops/my_cop.rb": "class MyCop; end\n",
		// processors and strategies a library looks up by name
		"lib/paperclip/blur.rb":            "module Paperclip\n  class Blur < Processor\n  end\nend\n",
		"lib/chewy/strategy/bypass.rb":     "class Chewy::Strategy\n  class Bypass < Base\n  end\nend\n",
		"lib/active_record/batches.rb":     "module ActiveRecord\n  module Batches\n    def pluck_each; end\n  end\nend\n",
		"lib/core_ext/string.rb":           "class String\n  def shout = upcase\nend\n",
		"app/views/accounts/show.rss.ruby": "RSS::Builder.build do |doc|\nend\n",
		"app/models/concerns/cacheable.rb": "module Cacheable\n  extend ActiveSupport::Concern\n  module ClassMethods\n    def cache_key; end\n  end\nend\n",
		"app/lib/signature.rb":             "class Signature < HTTP::Feature\n  HTTP::Options.register_feature(:sig, self)\nend\n",
		"app/lib/mixed.rb":                 "class Mine; end\n\nclass Paperclip::Extra; end\n",
		"app/models/admin/thing.rb":        "module Admin\n  class Thing; end\nend\n",
	}
	for f, c := range files {
		writeFile(t, root, f, c)
	}
	r := New()
	t.Cleanup(r.Close)
	for _, rel := range []string{
		"lib/linter/cop.rb", "lib/linter/other_cop.rb", "lib/linter/haml_dot.rb", "lib/custom_cops/my_cop.rb",
		".active_record_doctor.rb", "sorbet/tapioca/require.rb",
		"lib/paperclip/blur.rb", "lib/chewy/strategy/bypass.rb", "lib/active_record/batches.rb",
		"lib/core_ext/string.rb", "app/views/accounts/show.rss.ruby",
	} {
		if u := usage(t, r, root, rel); u.Entry == "" {
			t.Errorf("%s: not an entry", rel)
		}
	}
	for _, rel := range []string{"app/lib/mixed.rb", "app/models/admin/thing.rb", "app/models/concerns/cacheable.rb"} {
		if u := usage(t, r, root, rel); u.Entry != "" {
			t.Errorf("%s: entry %q, want none", rel, u.Entry)
		}
	}
	keep := func(rel, name string, want bool) {
		t.Helper()
		if got := slices.Contains(usage(t, r, root, rel).Keep, name); got != want {
			t.Errorf("%s: keep %s = %v, want %v", rel, name, got, want)
		}
	}
	keep("app/models/concerns/cacheable.rb", "Cacheable::ClassMethods", true) // extended by ActiveSupport::Concern
	keep("app/lib/signature.rb", "Signature", true)                           // registers itself
	keep("app/lib/mixed.rb", "Paperclip::Extra", true)
	keep("app/lib/mixed.rb", "Mine", false)

	// a class is a class even when it only nests others (Holder.new); a
	// module that only nests others is a namespace
	fg, err := r.ResolveFile(context.Background(), root, "lib/chewy/strategy/bypass.rb")
	if err != nil {
		t.Fatal(err)
	}
	if got := symbolSet(fg); !slices.Contains(got, "class Chewy::Strategy") {
		t.Errorf("symbols = %q", got)
	}
}

// The JavaScript of a Rails application starts where its asset tool
// (jsbundling, importmap, Vite Ruby, Shakapacker / Webpacker, Sprockets)
// says, not in package.json.
func TestRailsJavaScriptEntries(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"Gemfile":                                    "gem 'rails'\n",
		"package.json":                               `{"name": "app", "private": true}`,
		"config/importmap.rb":                        "pin \"application\"\npin \"autosize\", to: \"vendor/autosize.js\"\npin_all_from \"app/javascript/controllers\", under: \"controllers\"\n",
		"app/javascript/application.js":              "import './lib/x';\n",
		"app/javascript/lib/x.js":                    "export const x = 1;\n",
		"app/javascript/vendor/autosize.js":          "export default 1;\n",
		"app/javascript/controllers/hello.js":        "export default class {}\n",
		"app/javascript/entrypoints/admin.tsx":       "export {};\n",
		"app/javascript/orphan/y.js":                 "export const y = 1;\n",
		"app/assets/config/manifest.js":              "//= link_tree ../images\n",
		"app/assets/javascripts/legacy.js":           "var a = 1;\n",
		"engines/shop/Gemfile":                       "",
		"engines/shop/config/shakapacker.yml":        "default:\n  source_path: app/client\n  source_entry_path: bundles\n",
		"engines/shop/app/client/bundles/shop.js":    "export {};\n",
		"engines/shop/app/client/other.js":           "export {};\n",
		"engines/vite/Gemfile":                       "",
		"engines/vite/config/vite.json":              `{"all": {"sourceCodeDir": "frontend", "entrypointsDir": "starts"}}`,
		"engines/vite/frontend/starts/main.ts":       "export {};\n",
		"engines/vite/app/frontend/entrypoints/x.ts": "export {};\n",
	}
	for f, c := range files {
		writeFile(t, root, f, c)
	}
	r := New()
	t.Cleanup(r.Close)
	for _, rel := range []string{
		"app/javascript/application.js", "app/javascript/vendor/autosize.js", "app/javascript/controllers/hello.js",
		"app/javascript/entrypoints/admin.tsx", "app/assets/config/manifest.js", "app/assets/javascripts/legacy.js",
		"engines/shop/app/client/bundles/shop.js", "engines/vite/frontend/starts/main.ts",
	} {
		if u := usage(t, r, root, rel); u.Entry == "" {
			t.Errorf("%s: not an entry", rel)
		}
	}
	for _, rel := range []string{"app/javascript/lib/x.js", "app/javascript/orphan/y.js", "engines/shop/app/client/other.js", "engines/vite/app/frontend/entrypoints/x.ts"} {
		if u := usage(t, r, root, rel); u.Entry != "" {
			t.Errorf("%s: entry %q, want none", rel, u.Entry)
		}
	}
}

// Paths in configuration files never name anything outside the repository.
func TestRubyConfigPathsStayInRoot(t *testing.T) {
	parent := t.TempDir()
	root := parent + "/repo"
	writeFile(t, parent, "outside.rb", "class Outside; end\n")
	writeFile(t, root, "Gemfile", "")
	writeFile(t, root, ".rubocop.yml", "require:\n  - ../outside.rb\n")
	writeFile(t, root, "config/vite.json", `{"all": {"sourceCodeDir": "../..", "entrypointsDir": "."}}`)
	writeFile(t, root, "config/shakapacker.yml", "default:\n  source_path: ../../\n  source_entry_path: /\n")
	r := New()
	t.Cleanup(r.Close)
	m := r.manifests(context.Background(), root)
	for f := range m.files {
		if strings.HasPrefix(f, "../") || f == ".." {
			t.Errorf("entry outside the repository: %s", f)
		}
	}
	for p := range m.prefixes {
		if strings.HasPrefix(p, "../") || p == "./" || p == "../" {
			t.Errorf("entry prefix outside the repository: %s", p)
		}
	}
}

// Constants the repository does not declare are recorded by their
// top-level name: Rails code uses its frameworks through constants, not
// requires.
func TestRubyExternalConstants(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "Gemfile", "source 'https://rubygems.org'\n")
	writeFile(t, root, "app/models/user.rb", "class User < ApplicationRecord\nend\n")
	writeFile(t, root, "app/models/application_record.rb", "class ApplicationRecord < ActiveRecord::Base\n  self.abstract_class = true\nend\n")
	writeFile(t, root, "app/services/sync.rb", `class Sync
  def call
    User.find(1)
    Sidekiq::Client.push({})
    ::Aws::S3::Client.new
    JSON.parse("{}")
    Sync
  end
end
`)
	fg := resolve(t, root, "app/services/sync.rb")
	if fg.Usage == nil || !slices.Equal(fg.Usage.ExtConstants, []string{"Aws", "JSON", "Sidekiq"}) {
		t.Errorf("external constants = %v", fg.Usage.ExtConstants)
	}
	if got := resolve(t, root, "app/models/application_record.rb").Usage.ExtConstants; !slices.Equal(got, []string{"ActiveRecord"}) {
		t.Errorf("superclass: %v", got)
	}
}
