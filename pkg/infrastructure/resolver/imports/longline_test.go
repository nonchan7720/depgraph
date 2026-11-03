package imports

import (
	"strings"
	"testing"
)

// A line longer than bufio.Scanner's 64KB token limit must not cut the
// config short: the settings after it still apply.
func TestPythonConfigAfterLongLine(t *testing.T) {
	long := strings.Repeat("x", 70*1024)
	root := tree(t, map[string]string{
		"pyproject.toml":     "[project]\ndescription = \"" + long + "\"\n\n[tool.setuptools.package-dir]\n\"\" = \"lib\"\n",
		"lib/mypkg/core.py":  "",
		"tests/test_core.py": "",
		"sub/setup.cfg":      "[metadata]\ndescription = " + long + "\n\n[options]\npackage_dir =\n    = py\n",
		"sub/py/cfgpkg/m.py": "",
		"sub/bin/cli.py":     "",
	})
	runCases(t, NewPython(), root, []resolveCase{
		{"pyproject after long line", "tests/test_core.py", "mypkg.core", inRepo("lib/mypkg")},
		{"setup.cfg after long line", "sub/bin/cli.py", "cfgpkg.m", inRepo("sub/py/cfgpkg")},
	})
}
