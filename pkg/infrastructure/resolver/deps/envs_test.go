package deps

import (
	"reflect"
	"testing"
)

// sitePackages adds installed distributions (name -> files of its
// dist-info, "RECORD" / "top_level.txt" / "entry_points.txt") to files.
func sitePackages(files map[string]string, venv string, dists map[string]map[string]string) map[string]string {
	for dist, info := range dists {
		for name, body := range info {
			files[venv+"/lib/python3.12/site-packages/"+dist+".dist-info/"+name] = body
		}
	}
	return files
}

const pyproject = `[project]
name = "app"
dependencies = [
  "requests>=2.31",
  "PyYAML==6.0.1",
  "python-dateutil",
  "app[extra]",
  "not-installed>=1",
]

[project.optional-dependencies]
dev = ["pytest-cov", "black"]

[dependency-groups]
test = [
  "google-cloud-storage",
  {include-group = "dev"},
]

[tool.poetry.dependencies]
python = "^3.12"
django = "^5.0"

[tool.poetry.group.dev.dependencies]
types-requests = "*"
`

// The dependencies of pyproject.toml and of requirements files are the
// distributions whose import names the project's virtual environment gives;
// one that is not installed there, or that shares its only module with
// another distribution, is not known and left out.
func TestDeclaredPython(t *testing.T) {
	root := tree(t, sitePackages(map[string]string{
		"pyproject.toml":        pyproject,
		"requirements.txt":      "# pinned\nrequests==2.31.0\n-r other.txt\nDjango[argon2]>=5 ; python_version > '3.8'\ngit+https://example.com/x.git\n",
		"requirements/dev.txt":  "black\n",
		"svc/requirements.txt":  "requests\n",
		"svc/pyproject.toml":    "[project]\nname = \"svc\"\ndependencies = [\"requests\"]\n",
		"other/requirements.in": "requests\n",
	}, ".venv", map[string]map[string]string{
		"requests-2.31.0":            {"top_level.txt": "requests\n"},
		"PyYAML-6.0.1":               {"top_level.txt": "_yaml\nyaml\n"},
		"python_dateutil-2.9.0":      {"RECORD": "dateutil/__init__.py,sha256=x,1\ndateutil/tz/tz.py,,\npython_dateutil-2.9.0.dist-info/METADATA,,\n../../bin/x,,\n"},
		"pytest_cov-5.0.0":           {"top_level.txt": "pytest_cov\n", "entry_points.txt": "[pytest11]\npytest_cov = pytest_cov.plugin\n"},
		"black-24.0.0":               {"top_level.txt": "black\nblib2to3\n", "entry_points.txt": "[console_scripts]\nblack = black:main\nblackd = blackd:main\n"},
		"google_cloud_storage-2.0.0": {"RECORD": "google/cloud/storage/__init__.py,,\n"},
		"google_cloud_core-2.0.0":    {"RECORD": "google/cloud/client.py,,\n"},
		"Django-5.0":                 {"top_level.txt": "django\n"},
		"types_requests-2.31.0":      {"top_level.txt": "requests-stubs\n"},
	}))
	ms := Declared(root, []string{"svc"})
	var paths []string
	for _, m := range ms {
		paths = append(paths, m.Path)
	}
	if want := []string{"pyproject.toml", "requirements.txt", "requirements/dev.txt", "svc/pyproject.toml", "svc/requirements.txt"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("manifests = %q, want %q", paths, want)
	}
	m := byPath(t, ms, "pyproject.toml")
	// not-installed and google-cloud-storage (its only module, google, is another distribution's too) are not known
	if got, want := names(m), []string{"PyYAML", "black", "django", "pytest-cov", "python-dateutil", "requests", "types-requests"}; !reflect.DeepEqual(got, want) {
		t.Errorf("deps = %q, want %q", got, want)
	}
	if m.Ecosystem != PyPI || !reflect.DeepEqual(m.Languages(), []string{"python"}) {
		t.Errorf("manifest = %+v", m)
	}
	lines := map[string]int{}
	for _, d := range m.Deps {
		lines[d.Name] = d.Line
	}
	if want := map[string]int{"requests": 4, "PyYAML": 5, "python-dateutil": 6, "pytest-cov": 12, "black": 12, "django": 22, "types-requests": 25}; !reflect.DeepEqual(lines, want) {
		t.Errorf("lines = %v, want %v", lines, want)
	}
	for key, want := range map[string]string{
		"ext:yaml":              "PyYAML",
		"ext:dateutil.tz":       "python-dateutil",
		"ext:requests.adapters": "requests",
		"ext:django.db.models":  "django",
		"ext:google.cloud":      "",
		"ext:os":                "",
	} {
		if got := m.Provider(key); got != want {
			t.Errorf("Provider(%s) = %q, want %q", key, got, want)
		}
	}
	if got, want := names(byPath(t, ms, "requirements.txt")), []string{"Django", "requests"}; !reflect.DeepEqual(got, want) {
		t.Errorf("requirements.txt = %q, want %q", got, want)
	}
	// one environment: an import anywhere uses the distribution in every manifest that declares it
	got := NewIndex(ms).Users("python", "svc/app", "ext:requests.adapters")
	want := []Ref{{"pyproject.toml", "requests"}, {"requirements.txt", "requests"}, {"svc/pyproject.toml", "requests"}, {"svc/requirements.txt", "requests"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Users = %v, want %v", got, want)
	}
}

// Without a virtual environment in the repository nothing is known about
// what a distribution is imported as: nothing is reported.
func TestPythonWithoutEnvironment(t *testing.T) {
	root := tree(t, map[string]string{"requirements.txt": "requests\n"})
	for _, m := range Declared(root, nil) {
		if len(m.Deps) != 0 {
			t.Errorf("%s declares %v without an environment to read", m.Path, m.Deps)
		}
	}
}

func TestPythonFacts(t *testing.T) {
	root := tree(t, sitePackages(map[string]string{
		"requirements.txt": "left-over\ndjango-storages\ngunicorn\npytest-cov\ntypes-requests\npsycopg2\nDjango\n",
		"app/settings.py":  "INSTALLED_APPS = ['storages']\nDATABASES = {'ENGINE': 'django.db.backends.postgresql'}\n# psycopg2 is the driver\n",
		"Procfile":         "web: gunicorn app.wsgi\n",
		"pyproject.toml":   "[tool.x]\nuse = \"left-over\"\n",
		"docs/notes.md":    "left-over",
		// what an installed distribution imports is used: Django loads the driver
		"venv/lib/python3.12/site-packages/django/db/backends/postgresql/base.py": "try:\n    import psycopg2\nexcept ImportError:\n    pass\n",
	}, "venv", map[string]map[string]string{
		"left_over-1.0":       {"top_level.txt": "left_over\n"},
		"django_storages-1.0": {"top_level.txt": "storages\n"},
		"gunicorn-1.0":        {"top_level.txt": "gunicorn\n", "entry_points.txt": "[console_scripts]\ngunicorn = gunicorn.app:run\n"},
		"pytest_cov-1.0":      {"top_level.txt": "pytest_cov\n", "entry_points.txt": "[pytest11]\npytest_cov = pytest_cov.plugin\n"},
		"types_requests-1.0":  {"top_level.txt": "requests-stubs\n"},
		"psycopg2-1.0":        {"top_level.txt": "psycopg2\n"},
		"Django-5.0":          {"top_level.txt": "django\n"},
	}))
	ms, m := only(t, root)
	if m.Path != "requirements.txt" {
		t.Fatalf("manifest = %s", m.Path)
	}
	got := factsOf(root, ms, m, names(m), Lookup{})
	want := map[string]Fact{
		"left-over":       {},
		"django-storages": {Use: "named in app/settings.py as storages"},
		"gunicorn":        {Use: "named in Procfile"},
		"pytest-cov":      {Doubt: "it registers entry points (pytest11) that another package loads without an import"},
		"types-requests":  {Doubt: "type checkers read type stubs without an import"},
		"psycopg2":        {Use: "imported by Django, which is installed"},
		"Django":          {Use: "named in app/settings.py as django"},
	}
	if !reflect.DeepEqual(got, want) {
		for n, w := range want {
			if got[n] != w {
				t.Errorf("%s = %+v, want %+v", n, got[n], w)
			}
		}
		if len(got) != len(want) {
			t.Errorf("facts = %+v", got)
		}
	}
}

const gemfile = `source "https://rubygems.org"

gem "rails", "~> 7.1"
gem 'sidekiq'
gem "rack-cors", require: "rack/cors"
gem "rubocop", require: false
gem "leftover", :require => false
group :development do
  gem "bootsnap", require: false
  gem "not-vendored"
end
gem "local", path: "vendor/local"
`

func vendored(files map[string]string, gems map[string]map[string]string) map[string]string {
	for gem, lib := range gems {
		for name, body := range lib {
			files["vendor/bundle/ruby/3.3.0/gems/"+gem+"/"+name] = body
		}
	}
	return files
}

// The gems of a Gemfile are known when the bundle is installed inside the
// repository (vendor/bundle): what they are required as and the constants
// they declare are read from their files. Any other gem is left out.
func TestDeclaredRuby(t *testing.T) {
	root := tree(t, vendored(map[string]string{"Gemfile": gemfile}, map[string]map[string]string{
		"rails-7.1.0":     {"lib/rails.rb": "module Rails\nend\n"},
		"sidekiq-7.0.0":   {"lib/sidekiq.rb": "module Sidekiq\n  class Client\n  end\nend\n", "lib/sidekiq/api.rb": "module Sidekiq\nend\n", "lib/sidekiq/rails.rb": "module Sidekiq\n  class Rails < ::Rails::Engine\n  end\nend\n"},
		"rack-cors-2.0.0": {"lib/rack/cors.rb": "module Rack\n  class Cors\n  end\nend\n"},
		"rubocop-1.60.0":  {"lib/rubocop.rb": "module RuboCop\nend\n", "exe/rubocop": "#!/usr/bin/env ruby\n"},
		"leftover-1.0.0":  {"lib/leftover.rb": "module Leftover\nend\n"},
		"bootsnap-1.18.0": {"lib/bootsnap.rb": "module Bootsnap\nend\n", "lib/bootsnap/setup.rb": ""},
	}))
	m := byPath(t, Declared(root, nil), "Gemfile")
	if got, want := names(m), []string{"bootsnap", "leftover", "rack-cors", "rails", "rubocop", "sidekiq"}; !reflect.DeepEqual(got, want) {
		t.Errorf("deps = %q, want %q", got, want)
	}
	if m.Ecosystem != Gem || !reflect.DeepEqual(m.Languages(), []string{"ruby"}) {
		t.Errorf("manifest = %+v", m)
	}
	for key, want := range map[string]string{"ext:sidekiq": "sidekiq", "ext:rack": "rack-cors", "ext:bootsnap": "bootsnap", "ext:json": ""} {
		if got := m.Provider(key); got != want {
			t.Errorf("Provider(%s) = %q, want %q", key, got, want)
		}
	}
	if got, want := m.Constants("sidekiq"), []string{"Sidekiq"}; !reflect.DeepEqual(got, want) {
		t.Errorf("constants of sidekiq = %q, want %q", got, want)
	}
	if got, want := m.Constants("rack-cors"), []string{"Rack"}; !reflect.DeepEqual(got, want) {
		t.Errorf("constants of rack-cors = %q, want %q", got, want)
	}
	for _, d := range m.Deps {
		if d.Name == "leftover" && d.Line != 7 {
			t.Errorf("leftover at line %d, want 7", d.Line)
		}
	}

	ms := Declared(root, nil)
	got := factsOf(root, ms, m, []string{"leftover", "rubocop", "sidekiq", "bootsnap", "rack-cors"}, Lookup{})
	want := map[string]Fact{
		// required by nothing, and Bundler does not require it either
		"leftover": {},
		"bootsnap": {},
		"rubocop":  {},
		// Bundler.require loads it when the application boots
		"rack-cors": {Doubt: "Bundler requires it when the application boots, so it may act without being referenced"},
		// Rails loads its engine when the application boots
		"sidekiq": {Doubt: "it hooks into Rails (a railtie or an engine), which loads it without a reference"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("facts = %+v, want %+v", got, want)
	}
}

func TestRubyNamedByAFile(t *testing.T) {
	root := tree(t, vendored(map[string]string{
		"Gemfile":            "gem \"rubocop\", require: false\ngem \"puma\"\ngem \"leftover\", require: false\n",
		".rubocop.yml":       "require:\n  - rubocop-rails\n",
		"bin/lint":           "#!/bin/sh\nbundle exec rubocop\n",
		"Procfile":           "web: bundle exec puma -C config/puma.rb\n",
		"Gemfile.lock":       "GEM\n  specs:\n    leftover (1.0.0)\n",
		"config/leftover.rb": "# leftover is gone\n",
	}, map[string]map[string]string{
		"rubocop-1.60.0": {"lib/rubocop.rb": ""}, "puma-6.0.0": {"lib/puma.rb": ""}, "leftover-1.0.0": {"lib/leftover.rb": ""},
	}))
	ms, m := only(t, root)
	got := factsOf(root, ms, m, names(m), Lookup{})
	want := map[string]Fact{
		"rubocop":  {Use: "named in bin/lint"},
		"puma":     {Use: "named in Procfile"},
		"leftover": {},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("facts = %+v, want %+v", got, want)
	}
}
