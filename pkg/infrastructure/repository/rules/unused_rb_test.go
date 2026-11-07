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
		"app/services/report_formatter.rb":     "class ReportFormatter\nend\n\nclass LeftoverFormatter\nend\n",
		"app/services/importers/csv.rb":        "module Importers\n  class Csv\n  end\nend\n",
		"app/services/legacy_sync.rb":          "class LegacySync\nend\n",
		"app/views/users/index.html.erb":       "<%= render CardComponent.new(users: @users) %>\n",
		"app/components/card_component.rb":     "class CardComponent < ViewComponent::Base\nend\n",
		"package.yml":                          "enforce_dependencies: true\n",
		"packs/pay/package.yml":                "enforce_dependencies: true\nenforce_privacy: true\n",
		"packs/pay/app/public/pay/api.rb":      "module Pay\n  class Api\n    def self.charge\n      Charge.create\n    end\n  end\n\n  class Refunds\n  end\nend\n",
		"packs/pay/app/models/pay/charge.rb":   "module Pay\n  class Charge < ApplicationRecord\n  end\nend\n",
		"packs/pay/app/models/pay/ledger.rb":   "module Pay\n  class Ledger < ApplicationRecord\n  end\nend\n",
		"packs/pay/spec/models/charge_spec.rb": "RSpec.describe Pay::Charge do\nend\n",
	})
	got := evalUnused(toUnusedFiles(parseRepo(t, root)), rules.DefaultConfig())
	wantFindings(t, got,
		"unreachable-file|app/controllers/concerns/forgotten.rb",
		"unreachable-file|app/models/concerns/orphan.rb",
		"unreachable-file|app/services/legacy_sync.rb",
		"unreachable-file|packs/pay/app/models/pay/ledger.rb",
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
