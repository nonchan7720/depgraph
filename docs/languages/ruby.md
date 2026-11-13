# Ruby (Rails, engines, packwerk packs)

English | [日本語](ja/ruby.md) · [README](../../README.md) · other languages: [Go](go.md), [TypeScript / JavaScript](typescript.md), [Python](python.md)

What depgraph reads in Ruby code, and what the language-specific rules mean for it. The rules
themselves, their options and `.depgraph.yml` are described in [Rules](../rules.md) and [Configuration](../configuration.md).

Rails code names other files by constant, not by `require`: Zeitwerk loads `Admin::User` from
`admin/user.rb`. So depgraph treats constants as dependencies and uses, and it knows the conventions
Rails and its tools load code by.

## Files

- Ruby source: `.rb`, `.rake`, `config.ru` and `Rakefile`; `.jbuilder`, `.builder` and `.ruby` (the
  Ruby template handler) under `views/`.
- Views: ERB, Haml and Slim are not parsed as Ruby. The constants their code and attributes name are
  read (`<%= render CardComponent.new %>`, `%a{href: Admin::Routes.url(user)}`, `a href=X.url`).
- Not parsed, but read for the constants they name: extensionless Ruby scripts (`#!/usr/bin/env ruby`)
  and YAML under `config/`.

## Import resolution

- `require_relative` is relative to the file.
- `require` uses the load path the repository implies: `lib/`, `spec/` and `test/` of the file's
  project (the directory of its `Gemfile` / `*.gemspec`), then `lib/` of every gem of the repository.
  A `require` that leaves the repository is never followed.
- **Constants** are resolved the way Zeitwerk loads them. The autoload roots are every directory under
  `app/` (but `assets`, `javascript` and `views`), its `concerns/`, and `lib/`. They are taken from:
  - the application
  - its engines
  - its packwerk packs
  - any directory whose `app/` has Rails' layout (a Discourse plugin)

  `Admin::UsersController` is `admin/users_controller.rb` under one of the roots. It is looked up in the
  lexical nesting it is written in (`Admin::UsersController::User`, `Admin::User`, `User`). A constant
  that Ruby or a gem defines is no dependency.

The application, every gem / engine (`*.gemspec`) and every packwerk pack (`package.yml`) is a module,
one of the units of `module-cycle`. A reference from one pack to another is an import between them, so
`module-cycle` sees a cycle of packs. packwerk itself checks only the declared dependencies.

## Third-party packages (`ext:` in layers)

A `require` or a constant is third-party when its name is a gem that the `Gemfile` asks for (the
`DEPENDENCIES` of the bundle's `Gemfile.lock`: `rails`, `sidekiq`, `sidekiq/api`). A constant is judged
by its top-level name (`ActiveRecord` of `ActiveRecord::Base`). Anything else is unknown:

- A gem that is locked only as another gem's dependency (`activerecord` under `rails`, but also default
  gems such as `json` and `net-http`) is listed by `external-unclassified`. Name it in an `ext:` pattern
  to check it.
- A constant that is probably a gem's but not by its name (`ActionController` of `actionpack`, `Aws` of
  `aws-sdk-s3`) is listed too.
- One that is likely Ruby's own (`Hash`, `IO`) is not listed: telling them apart would take a list of
  Ruby's constants per version.

See [Frameworks](../configuration.md#frameworks-external-packages-in-layers).

## Unused code

### What is reported

- `unused-export`: classes, modules with something in them, and constants that are classes
  (`Struct.new`, `Class.new`, `Data.define`). Methods are never reported: callbacks, routes, views and
  `send` call them by name.
- `unreachable-file`: a Ruby file that no entry point reaches.
- A module that only holds other classes is a namespace: there is nothing in it to use.

### What counts as a use

- **The constant's name**, wherever it is written:
  - `User`, `Admin::Role`, a superclass, `include` / `extend` of a concern
  - strings and symbols: `"Admin::User"`, `class_name: "User"` / `class_name: :Note`, `const_get(:Thing)`
  - word arrays: `%w[CsvImporter JsonImporter]`, `delegated_type types: %w[Message Comment]`
- **Associations**: `has_many :comments` uses `Comment`, singularized with ActiveSupport's rules. A name
  derived from a symbol also matches an acronym class such as `APIKey`.
- **Validators**: `validates :email, email: true` (`EmailValidator`), `'gitlab/emoji_name': true`
  (`Gitlab::EmojiNameValidator`).
- **Names built at run time**:
  - `"Importers::#{kind}".constantize` uses every constant under `Importers`.
  - `"#{module_parent_name}::#{kind}".constantize` uses every constant of the enclosing namespace.
  - `const_get("#{name.camelize}Pipeline")` uses every constant of its own namespace that ends with
    `Pipeline`.
  - `has_one :"#{name}_search_data"` uses every constant that ends with `SearchData`.
  - A suffix alone does not count outside `const_get` (`"#{kind}Service".constantize` does not use every
    `*Service`).
- **Files**:
  - `require` makes a file reachable.
  - A glob of Ruby files (`Dir["lib/patches/*.rb"]`) uses every file under its directory. It is relative
    to the file, its project, or (if not recursive) the repository root. `require_rel` is relative to the
    file only. Globs in tests only scan files and load nothing.
  - Zeitwerk loads the file of a namespace (`admin.rb`, even one that is only `module Vacuum; end`) for
    its constants. Files that only reopen the namespace to nest their own classes are not loaded for it.
- **Views and configuration**: the constants that views, `config/` YAML (`validator: "ColorListValidator"`)
  and extensionless scripts name.

### Entry points

What Rails or a tool loads by convention:

- `app/`: `controllers`, `jobs`, `mailers`, `channels`, `helpers`, `mailboxes`, `views`, `workers`, and the
  classes that tools infer from a model's name (`policies`, `serializers`, `decorators`, `dashboards`,
  `inputs`, `admin`). The `concerns/` under them are not entry points.
- `config/`, `db/`, `bin/`, `script/`, `lib/tasks`, `lib/generators`, `Rakefile`, `config.ru`, `*.rake`,
  `sorbet/` and `locale/` (gettext).
- Tests: `spec/`, `test/`, `*_spec.rb`, `*_test.rb`.
- Scripts: `#!`, `if __FILE__ == $0`, `bundler/inline`.
- Tooling:
  - dot-directories (`.skills/`) and dotfiles (`.active_record_doctor.rb`)
  - the files that RuboCop / Standard / haml-lint configurations `require`
- Discourse plugins: `plugin.rb`.
- A gem's `lib/` is its public API.
- A packwerk pack's public path (`public_path` of `package.yml`, `app/public/` by default) is reachable.
  Packs are used by the application only, so a class of the public path that nothing uses is still
  reported.

### Never reported

- A class or module that reopens a library's namespace. The libraries are Ruby, Rails, and the gems of
  `Gemfile` / `Gemfile.lock`, matched whatever the case (`OmniAuth` of `omniauth`), for example
  `Paperclip`, `Chewy`, `ActiveRecord`, `String`. The library loads such classes by name. A gem named
  like the application's own namespace does not count: `lib/gitlab.rb` next to the `gitlab` gem makes
  `Gitlab::*` the application's.
- STI subclasses in `models/` (loaded from the `type` column).
- The application's abstract `ApplicationRecord`.
- Subclasses of `Rails::Railtie`, `Rails::Engine` and generators, and classes registered by a
  class-body DSL (`register_for 'x'`).
- Classes and modules that register or prepend `self` (`Object.prepend(self)`).
- A concern's `ClassMethods`.

A concern is not an entry point, wherever it lives: a concern that nothing includes is reported.

### JavaScript of a Rails application

The JavaScript of a Rails application starts where its asset tool says:

- Sprockets: `app/assets/`
- jsbundling and importmap: the files directly in `app/javascript/`
- importmap: the pins and `pin_all_from` directories of `config/importmap.rb`
- Vite Ruby: `config/vite.json`, else `app/javascript/entrypoints` and `app/frontend/entrypoints`
- Shakapacker / Webpacker: `source_path` / `source_entry_path`, else `app/javascript/packs`
- Discourse plugins: their `assets/javascripts`, which the Ember resolver loads by name

## Other rules

- `duplicate-symbol` leaves Ruby out: reopening a class is how Ruby extends it.
- `duplicate-code` compares methods (`Class#name`, `Class.name`), not whole classes.
- `concrete-dependency`, `type-leak` and `low-cohesion` do not apply to Ruby. It has no types to read
  and no interfaces but duck typing, and the parts of a class would be methods called by dynamic
  dispatch: depgraph does not guess.

## Known limits

- Files in convention directories (controllers, helpers, serializers, policies, ...) are entry points,
  so unused code there is not reported. This trades missed findings for no false positives.
- Code loaded by a name built in other ways (a suffix alone, a name from the database) needs a
  `# depgraph:entry` comment or `entries:` in `.depgraph.yml`.
- Acronyms from `config/initializers/inflections.rb` are not read. Names derived from symbols match
  case-insensitively instead, which covers associations and validators.
- There is no language server for Ruby: calls and references between methods are not added.
