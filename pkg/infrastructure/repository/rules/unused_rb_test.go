package rules

import (
	"testing"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// A Rails application with concerns, a view, an STI model, a constant name
// built at run time and a packwerk pack: only what nothing reaches or uses
// is reported.
func TestUnusedRubyRails(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"Gemfile":               "source 'https://rubygems.org'\ngem 'rails'\n",
		"config/application.rb": "require_relative 'boot'\nmodule App\n  class Application < Rails::Application; end\nend\n",
		"config/boot.rb":        "",
		"config/routes.rb":      "Rails.application.routes.draw do\n  resources :users\nend\n",
		"app/controllers/application_controller.rb": "class ApplicationController < ActionController::Base\n  include Authentication\nend\n",
		"app/controllers/users_controller.rb": `class UsersController < ApplicationController
  def index
    @users = User.all
    Report.new.call
    Pay::Api.charge
    Admin::Audit.log
    Pay.config
    Vacuum::Imports.run
  end
end
`,
		"app/controllers/concerns/authentication.rb": "module Authentication\n  extend ActiveSupport::Concern\n  included do\n    before_action :login\n  end\nend\n",
		"app/controllers/concerns/forgotten.rb":      "module Forgotten\n  extend ActiveSupport::Concern\nend\n",
		"app/models/application_record.rb":           "class ApplicationRecord < ActiveRecord::Base\n  primary_abstract_class\nend\n",
		"app/models/user.rb": `class User < ApplicationRecord
  include Taggable
  include Searchable
  has_many :comments
end
`,
		"app/models/admin_user.rb":               "class AdminUser < User\nend\n", // STI
		"app/models/comment.rb":                  "class Comment < ApplicationRecord\nend\n",
		"app/models/concerns/taggable.rb":        "module Taggable\n  extend ActiveSupport::Concern\n  included do\n    has_many :tags\n  end\nend\n",
		"app/models/tag.rb":                      "class Tag < ApplicationRecord\nend\n",
		"app/models/concerns/user/searchable.rb": "class User\n  module Searchable\n    extend ActiveSupport::Concern\n  end\nend\n",
		"app/models/concerns/orphan.rb":          "module Orphan\n  extend ActiveSupport::Concern\nend\n",
		// loaded by Zeitwerk for Admin::Audit: the namespace's own file
		"app/models/admin.rb": "module Admin\n  def self.table_name_prefix\n    'admin_'\n  end\nend\n",
		// an explicit namespace file with nothing in it: Zeitwerk loads it
		"app/lib/vacuum.rb":         "module Vacuum; end\n",
		"app/lib/vacuum/imports.rb": "class Vacuum::Imports\n  def self.run; end\nend\n",
		"app/models/admin/audit.rb": "module Admin\n  class Audit < ApplicationRecord\n  end\nend\n",
		"app/services/report.rb": `class Report
  def call
    ReportFormatter.new
    "Importers::#{kind.camelize}".constantize
  end
end
`,
		"app/services/report_formatter.rb":   "class ReportFormatter\nend\n\nclass LeftoverFormatter\nend\n",
		"app/services/importers/csv.rb":      "module Importers\n  class Csv\n  end\nend\n",
		"app/services/legacy_sync.rb":        "class LegacySync\nend\n",
		"app/views/users/index.html.erb":     "<%= render CardComponent.new(users: @users) %>\n",
		"app/components/card_component.rb":   "class CardComponent < ViewComponent::Base\nend\n",
		"package.yml":                        "enforce_dependencies: true\n",
		"packs/pay/package.yml":              "enforce_dependencies: true\nenforce_privacy: true\n",
		"packs/pay/app/public/pay/api.rb":    "module Pay\n  class Api\n    def self.charge\n      Charge.create\n    end\n  end\n\n  class Refunds\n  end\nend\n",
		"packs/pay/app/models/pay/charge.rb": "module Pay\n  class Charge < ApplicationRecord\n  end\nend\n",
		"packs/pay/app/models/pay/ledger.rb": "module Pay\n  class Ledger < ApplicationRecord\n  end\nend\n",
		// naming the namespace itself does not reach the files that only nest
		// their classes in it
		"packs/pay/app/services/pay/config.rb": "module Pay\n  def self.config = {}\nend\n",
		// an abstract base nothing inherits from is unused (only the
		// application's own ApplicationRecord is kept)
		"app/models/statistics_record.rb":      "class StatisticsRecord < ApplicationRecord\n  self.abstract_class = true\nend\n",
		"packs/pay/spec/models/charge_spec.rb": "RSpec.describe Pay::Charge do\nend\n",
	})
	got := evalUnused(toUnusedFiles(parseRepo(t, root)), rules.DefaultConfig())
	wantFindings(t, got,
		"unreachable-file|app/controllers/concerns/forgotten.rb",
		"unreachable-file|app/models/concerns/orphan.rb",
		"unreachable-file|app/services/legacy_sync.rb",
		"unreachable-file|packs/pay/app/models/pay/ledger.rb",
		"unreachable-file|app/models/statistics_record.rb",
		"unused-export|app/services/report_formatter.rb#LeftoverFormatter",
		"unused-export|packs/pay/app/public/pay/api.rb#Pay::Refunds",
	)
}

// A gem's lib/ is its public API; constants resolve through requires and
// lexical scope without Rails.
func TestUnusedRubyGem(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"money.gemspec":       "Gem::Specification.new do |s|\n  s.name = 'money'\nend\n",
		"lib/money.rb":        "require 'money/rate'\nmodule Money\nend\n",
		"lib/money/rate.rb":   "module Money\n  class Rate\n  end\nend\n",
		"spec/rate_spec.rb":   "require 'spec_helper'\nRSpec.describe Money::Rate do\nend\n",
		"spec/spec_helper.rb": "require 'money'\n",
		"Rakefile":            "require 'bundler/gem_tasks'\n",
	})
	wantFindings(t, evalUnused(toUnusedFiles(parseRepo(t, root)), rules.DefaultConfig()))
}

// Review findings (false positives): each of these files is used.
func TestUnusedRubyReviewCases(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"Gemfile":                "gem 'rails'\ngem 'omniauth'\ngem 'api-pagination'\n",
		"config/application.rb":  "",
		".rubocop.yml":           "require: ./lib/custom_cops/foo.rb\n",
		"lib/custom_cops/foo.rb": "class FooCop; end\n",
		"app/controllers/users_controller.rb": `class UsersController < ApplicationController
  KINDS = %w[CsvImporter JsonImporter]
  def index
    Holder.new
    User.first
    Object.const_get(:Thing)
    KINDS.map(&:constantize)
  end
end
`,
		"app/services/holder.rb":           "class Holder\n  class Inner; end\nend\n",
		"app/services/csv_importer.rb":     "class CsvImporter; end\n",
		"app/services/json_importer.rb":    "class JsonImporter; end\n",
		"app/services/thing.rb":            "class Thing; end\n",
		"app/services/legacy.rb":           "unless defined?(Legacy)\n  class Legacy; end\nend\n",
		"app/models/application_record.rb": "class ApplicationRecord < ActiveRecord::Base\n  primary_abstract_class\nend\n",
		"app/models/user.rb": `class User < ApplicationRecord
  has_many :api_keys
  has_many :notes, class_name: :Note
  delegated_type :entryable, types: %w[Message Comment]
  validates :url, url: true
  Legacy
end
`,
		"app/models/api_key.rb":                "class APIKey < ApplicationRecord; end\n",
		"app/validators/url_validator.rb":      "class URLValidator < ActiveModel::EachValidator; end\n",
		"app/models/note.rb":                   "class Note < ApplicationRecord; end\n",
		"app/models/message.rb":                "class Message < ApplicationRecord; end\n",
		"app/models/comment.rb":                "class Comment < ApplicationRecord; end\n",
		"app/views/users/index.html.haml":      "%a{href: Admin::Routes.url_for(user)} Link\n%div{class: Css::BAR}\n%a(href=Baz::Y.z) Link\n",
		"app/views/users/show.html.slim":       "a href=Slimmy::Helper.url(user) Link\ndiv class=Css2::BAR\n",
		"app/services/admin/routes.rb":         "module Admin\n  class Routes; end\nend\n",
		"app/services/css.rb":                  "module Css\n  BAR = 'x'\nend\n",
		"app/services/baz/y.rb":                "module Baz\n  class Y; end\nend\n",
		"app/services/slimmy/helper.rb":        "module Slimmy\n  class Helper; end\nend\n",
		"app/services/css2.rb":                 "module Css2\n  BAR = 'x'\nend\n",
		"app/lib/omni_auth/strategies/acme.rb": "module OmniAuth\n  module Strategies\n    class Acme < OmniAuth::Strategies::OAuth2; end\n  end\nend\n",
		// the application's own Api namespace is not the api-pagination gem's
		"app/services/api/unused_client.rb": "module Api\n  class UnusedClient; end\nend\n",
		"Dockerfile.builder":                "FROM ruby AS build\nRUN make\n",
	})
	got := evalUnused(toUnusedFiles(parseRepo(t, root)), rules.DefaultConfig())
	wantFindings(t, got,
		"unreachable-file|app/services/api/unused_client.rb",
		"unused-export|app/services/holder.rb#Holder::Inner", // nothing uses it
	)
}

// A fresh application (no model yet) does not report its abstract base.
func TestUnusedRubyFreshApp(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"Gemfile":                          "gem 'rails'\n",
		"config/application.rb":            "",
		"app/models/application_record.rb": "class ApplicationRecord < ActiveRecord::Base\n  self.abstract_class = true\nend\n",
	})
	wantFindings(t, evalUnused(toUnusedFiles(parseRepo(t, root)), rules.DefaultConfig()))
}

// Conventions of Discourse and its plugins (a plugin is a Rails engine
// without a Gemfile, loaded by plugin.rb; its JavaScript is loaded by the
// Ember resolver), and ways Rails code names files and classes outside the
// constants it writes.
func TestUnusedRubyDiscourseCases(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"Gemfile":                         "gem 'rails'\n",
		"config/application.rb":           "",
		"plugins/chat/plugin.rb":          "module ::Chat\n  PLUGIN_NAME = 'chat'\nend\nrequire_relative 'lib/chat/engine'\nafter_initialize do\nend\n",
		"plugins/chat/lib/chat/engine.rb": "module Chat\n  class Engine < ::Rails::Engine\n    isolate_namespace Chat\n  end\nend\n",
		"plugins/chat/app/controllers/chat/api_controller.rb":                  "module Chat\n  class ApiController < ::ApplicationController\n    def index\n      Chat::Channel.all\n    end\n  end\nend\n",
		"plugins/chat/app/models/chat/channel.rb":                              "module Chat\n  class Channel < ActiveRecord::Base\n  end\nend\n",
		"plugins/chat/app/models/chat/orphan.rb":                               "module Chat\n  class Orphan < ActiveRecord::Base\n  end\nend\n",
		"plugins/chat/package.json":                                            `{"name": "chat", "private": true}`,
		"plugins/chat/assets/javascripts/discourse/initializers/chat.js":       "export default { name: 'chat' };\n",
		"plugins/chat/admin/assets/javascripts/discourse/routes/admin-chat.js": "export default class AdminChatRoute {}\n",
		// required by a glob
		"config/initializers/002-freedom_patches.rb": "Dir[\"#{Rails.root.join(\"lib/freedom_patches/*.rb\")}\"].each { |f| require f }\n" +
			"Rails.application.config.paths.add(\"plugins/*\", eager_load: false)\n", // a directory glob is no use
		"lib/freedom_patches/copy_file.rb": "module FreedomPatches\n  module CopyFile\n    def copy; end\n  end\nend\n",
		// named in configuration
		"config/site_settings.yml":               "basic:\n  colors:\n    default: ''\n    validator: \"ColorListValidator\"\n",
		"lib/validators/color_list_validator.rb": "class ColorListValidator\n  def valid_value?(v) = true\nend\n",
		// an association whose name is built at run time
		"app/models/concerns/searchable.rb":  "module Searchable\n  extend ActiveSupport::Concern\n  included { has_one :\"#{name.underscore}_search_data\", dependent: :destroy }\nend\n",
		"app/models/application_record.rb":   "class ApplicationRecord < ActiveRecord::Base\n  primary_abstract_class\nend\n",
		"app/models/tag.rb":                  "class Tag < ApplicationRecord\n  include Searchable\nend\n",
		"app/models/tag_search_data.rb":      "class TagSearchData < ApplicationRecord\nend\n",
		"app/controllers/tags_controller.rb": "class TagsController < ApplicationController\n  def index\n    Tag.all\n  end\nend\n",
		// a module that prepends itself
		"config/boot.rb":        "require_relative '../lib/require_compat'\n",
		"lib/require_compat.rb": "module RequireCompat\n  def require_dependency(f) = super\n  Object.prepend(self)\nend\n",
		// used by an extensionless Ruby script
		"docs/guides/sync_docs":   "#!/usr/bin/env ruby\nrequire_relative 'lib/util'\nUtil.parse_md('x')\n",
		"docs/guides/lib/util.rb": "module Util\n  def self.parse_md(s) = s\nend\n",
		// a script of a tool directory
		".skills/upcoming/scripts/optimize.rb": "puts 1\n",
		// a standalone script that brings its own gems
		"docs/guides/scrape_docs.rb": "require \"bundler/inline\"\ngemfile do\n  gem \"faraday\"\nend\nputs 1\n",
		// classes of its own namespace looked up by name
		"lib/service/steps_inspector.rb": `module Service
  class StepsInspector
    class Step
      def inspector
        "#{module_parent_name}::#{self.class.name.demodulize}".constantize.new
      end
    end
    class Policy < Step; end
    class Lock < Step; end
  end
end
`,
		"app/services/runner.rb":             "class Runner\n  def call = Service::StepsInspector::Step.new.inspector\nend\n",
		"app/controllers/runs_controller.rb": "class RunsController < ApplicationController\n  def index = Runner.new.call\nend\n",
	})
	got := evalUnused(toUnusedFiles(parseRepo(t, root)), rules.DefaultConfig())
	wantFindings(t, got, "unreachable-file|plugins/chat/app/models/chat/orphan.rb")
}
