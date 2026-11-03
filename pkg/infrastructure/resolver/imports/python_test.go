package imports

import "testing"

func TestPythonRegression(t *testing.T) {
	root := tree(t, map[string]string{
		"app/__init__.py":          "",
		"app/models.py":            "",
		"app/services/__init__.py": "",
		"app/services/user.py":     "from ..models import User\n",
		"app/services/mail.py":     "",
		"run.py":                   "import app.models\n",
	})
	runCases(t, NewPython(), root, []resolveCase{
		{"parent relative module", "app/services/user.py", "..models", inRepo("app")},
		{"sibling relative module", "app/services/user.py", ".mail", inRepo("app/services")},
		{"relative package", "app/services/user.py", ".", inRepo("app/services")},
		{"relative parent package", "app/services/user.py", "..", inRepo("app")},
		{"absolute module", "run.py", "app.models", inRepo("app")},
		{"absolute package", "run.py", "app.services", inRepo("app/services")},
		{"stdlib", "run.py", "os.path", ext("os.path")},
		{"third party", "run.py", "requests", ext("requests")},
	})
}

func TestPythonSrcLayout(t *testing.T) {
	root := tree(t, map[string]string{
		"src/pkg/__init__.py": "",
		"src/pkg/mod.py":      "",
		"tests/test_mod.py":   "import pkg.mod\n",
	})
	runCases(t, NewPython(), root, []resolveCase{
		{"src layout", "tests/test_mod.py", "pkg.mod", inRepo("src/pkg")},
	})
}

func TestPythonNestedPackageRoot(t *testing.T) {
	// backend/app is imported as "app.x": the package root is backend/
	root := tree(t, map[string]string{
		"backend/app/__init__.py":      "",
		"backend/app/core/__init__.py": "",
		"backend/app/core/config.py":   "",
		"backend/app/api/__init__.py":  "",
		"backend/app/api/routes.py":    "from app.core.config import settings\n",
		"libs/common/__init__.py":      "",
		"libs/common/text.py":          "",
		"scripts/run.py":               "import common.text\n",
	})
	runCases(t, NewPython(), root, []resolveCase{
		{"own top-level package", "backend/app/api/routes.py", "app.core.config", inRepo("backend/app/core")},
		{"own package dir", "backend/app/api/routes.py", "app.core", inRepo("backend/app/core")},
		{"other top-level package", "scripts/run.py", "common.text", inRepo("libs/common")},
		{"missing module in known package root stays external", "scripts/run.py", "nothing.here", ext("nothing.here")},
	})
}

func TestPythonPyprojectPackageDir(t *testing.T) {
	// mypkg has no __init__.py (namespace package): only package-dir finds it
	root := tree(t, map[string]string{
		"pyproject.toml": `[project]
name = "x"

[tool.setuptools.package-dir]
"" = "lib"
`,
		"lib/mypkg/core.py":  "",
		"tests/test_core.py": "import mypkg.core\n",
	})
	runCases(t, NewPython(), root, []resolveCase{
		{"setuptools package-dir", "tests/test_core.py", "mypkg.core", inRepo("lib/mypkg")},
	})
}

func TestPythonPyprojectFindWhere(t *testing.T) {
	root := tree(t, map[string]string{
		"services/api/pyproject.toml": `[tool.setuptools.packages.find]
where = [
  "python",
]
`,
		"services/api/python/svc/handlers.py": "",
		"services/api/tests/test_h.py":        "",
	})
	runCases(t, NewPython(), root, []resolveCase{
		{"packages.find where in sub project", "services/api/tests/test_h.py", "svc.handlers", inRepo("services/api/python/svc")},
	})
}

func TestPythonPoetryPackages(t *testing.T) {
	root := tree(t, map[string]string{
		"pyproject.toml": `[tool.poetry]
name = "proj"
packages = [
    { include = "proj", from = "source" },
]
`,
		"source/proj/main.py": "",
		"bin/cli.py":          "",
	})
	runCases(t, NewPython(), root, []resolveCase{
		{"poetry from", "bin/cli.py", "proj.main", inRepo("source/proj")},
	})
}

func TestPythonHatchPackages(t *testing.T) {
	root := tree(t, map[string]string{
		"pyproject.toml": `[tool.hatch.build.targets.wheel]
packages = ["code/hpkg"]
`,
		"code/hpkg/m.py": "",
		"bin/cli.py":     "",
	})
	runCases(t, NewPython(), root, []resolveCase{
		{"hatch packages", "bin/cli.py", "hpkg.m", inRepo("code/hpkg")},
	})
}

func TestPythonSetupCfg(t *testing.T) {
	root := tree(t, map[string]string{
		"setup.cfg": `[metadata]
name = x

[options]
package_dir =
    = py
packages = find:
`,
		"py/cfgpkg/m.py": "",
		"bin/cli.py":     "",
	})
	runCases(t, NewPython(), root, []resolveCase{
		{"setup.cfg package_dir", "bin/cli.py", "cfgpkg.m", inRepo("py/cfgpkg")},
	})
}

func TestPythonUnresolvedRelative(t *testing.T) {
	root := tree(t, map[string]string{
		"app/__init__.py": "",
		"app/a.py":        "from .nope import x\n",
	})
	runCases(t, NewPython(), root, []resolveCase{
		{"missing relative is unresolved, not external", "app/a.py", ".nope", unresolved()},
		{"beyond the repo root", "app/a.py", "....x", unresolved()},
	})
}

func TestPythonInvalidate(t *testing.T) {
	root := tree(t, map[string]string{
		"lib/newpkg/m.py": "",
		"main.py":         "",
	})
	r := NewPython()
	runCases(t, r, root, []resolveCase{{"before", "main.py", "newpkg.m", ext("newpkg.m")}})
	writeFiles(t, root, map[string]string{"pyproject.toml": "[tool.setuptools.package-dir]\n\"\" = \"lib\"\n"})
	r.Invalidate(root)
	runCases(t, r, root, []resolveCase{{"after", "main.py", "newpkg.m", inRepo("lib/newpkg")}})
}
