package imports

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

func TestReadPythonVersionFile(t *testing.T) {
	for _, tc := range []struct {
		name, file, content, want string // want: minor version or "error: <substring>"
	}{
		{"python-version", ".python-version", "3.12.4\n", "3.12"},
		{"python-version comment", ".python-version", "# pinned\n3.11\n", "3.11"},
		{"python-version several", ".python-version", "3.12\n3.11\n", "error: several versions"},
		{"python-version pypy", ".python-version", "pypy3.10\n", "error: not an exact CPython version"},
		{"python-version venv name", ".python-version", "myenv\n", "error: not an exact CPython version"},
		{"python-version system", ".python-version", "system\n", "error: not an exact CPython version"},
		{"tool-versions", ".tool-versions", "nodejs 22.1.0\npython 3.12.1\n", "3.12"},
		{"tool-versions several", ".tool-versions", "python 3.12.1 3.11.9\n", "error: several versions"},
		{"tool-versions none", ".tool-versions", "nodejs 22\n", "error: no python"},
		{"mise", "mise.toml", "[tools]\nnode = \"22\"\npython = \"3.13\"\n", "3.13"},
		{"mise table", "mise.toml", "[tools]\npython = { version = \"3.12.2\" }\n", "3.12"},
		{"mise array", "mise.toml", "[tools]\npython = [\"3.12\", \"3.11\"]\n", "error: several versions"},
		{"mise latest", ".mise.toml", "[tools]\npython = \"latest\"\n", "error: not an exact CPython version"},
		{"mise prefix", "mise.toml", "[tools]\npython = \"prefix:3.12\"\n", "error: not an exact CPython version"},
		{"mise major", "mise.toml", "[tools]\npython = \"3\"\n", "error: not an exact CPython version"},
		{"mise dotted key", "mise.toml", "[tools]\n\"python\" = \"3.12\"\n", "3.12"},
		{"pyproject pyright", "pyproject.toml", "[project]\nrequires-python = \">=3.10\"\n[tool.pyright]\npythonVersion = \"3.12\"\n", "3.12"},
		{"pyproject mypy", "pyproject.toml", "[tool.mypy]\npython_version = \"3.11\"\n", "3.11"},
		{"pyproject disagree", "pyproject.toml", "[tool.pyright]\npythonVersion = \"3.12\"\n[tool.mypy]\npython_version = \"3.11\"\n", "error: disagree"},
		{"pyproject range only", "pyproject.toml", "[project]\nrequires-python = \">=3.10\"\n", "error: no exact python version"},
		{"pyrightconfig", "pyrightconfig.json", `{"pythonVersion": "3.10"}`, "3.10"},
		{"mypy.ini", "mypy.ini", "[mypy]\npython_version = 3.9\n", "3.9"},
		{"setup.cfg", "setup.cfg", "[metadata]\nname = x\n[mypy]\npython_version = 3.9\n", "3.9"},
		{"Dockerfile", "Dockerfile", "FROM python:3.12-slim-bookworm\nRUN pip install x\n", "3.12"},
		{"Dockerfile digest", "Dockerfile", "FROM docker.io/library/python:3.11.9@sha256:abc AS base\n", "3.11"},
		{"Dockerfile multi-stage", "Dockerfile", "FROM python:3.12 AS build\nFROM python:3.12-slim\n", "error: several FROM"},
		{"Dockerfile ARG", "Dockerfile", "ARG V=3.12\nFROM python:${V}\n", "error: not an exact python image"},
		{"Dockerfile other image", "Dockerfile", "FROM node:22\n", "error: not an exact python image"},
		{"unknown file", "versions.txt", "3.12\n", "error: unsupported version file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, tc.file), []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			v, err := ReadPythonVersion(root, rules.LanguageSetting{Dir: ".", Lang: "python", VersionFile: tc.file})
			if want, isErr := strings.CutPrefix(tc.want, "error: "); isErr {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("= %+v, %v; want error %q", v, err, want)
				}
				if strings.Contains(err.Error(), tc.content) && len(tc.content) > 4 {
					t.Errorf("the error quotes the file: %v", err)
				}
				return
			}
			if err != nil || v.Minor != tc.want || v.Source != tc.file {
				t.Errorf("= %+v, %v; want %s from %s", v, err, tc.want, tc.file)
			}
		})
	}
}

func TestReadPythonVersionLiteralAndComponent(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"lib/foo/.python-version": "3.9.18\n"})
	v, err := ReadPythonVersion(root, rules.LanguageSetting{Dir: "lib/foo", Lang: "python", VersionFile: ".python-version"})
	if err != nil || v.Minor != "3.9" || v.Source != "lib/foo/.python-version" {
		t.Errorf("component file: %+v, %v", v, err)
	}
	v, err = ReadPythonVersion(root, rules.LanguageSetting{Dir: ".", Lang: "python", Version: "3.12.1"})
	if err != nil || v.Minor != "3.12" || v.Source != "" {
		t.Errorf("literal: %+v, %v", v, err)
	}
	if _, err := ReadPythonVersion(root, rules.LanguageSetting{Dir: ".", Lang: "python", VersionFile: "missing"}); err == nil {
		t.Error("a missing version file is an error")
	}
}

// Between a file and the directory of the setting that applies to it,
// another file that pins a different version is a conflict depgraph does not
// settle; one that pins the same version is fine.
func TestPythonVersionConflict(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".python-version":           "3.12\n",
		"app/x.py":                  "",
		"lib/foo/.python-version":   "3.9\n", // its own pin, not configured
		"lib/foo/foo/core.py":       "",
		"lib/bar/mise.toml":         "[tools]\npython = \"3.12.1\"\n", // same version
		"lib/bar/bar.py":            "",
		"lib/env/mise.staging.toml": "[tools]\npython = \"3.12\"\n", // an environment's config
		"lib/env/e.py":              "",
		"docs/pyproject.toml":       "[project]\nrequires-python = \">=3.8\"\n", // a range is no pin
		"docs/conf.py":              "",
	})
	s := rules.LanguageSetting{Dir: ".", Lang: "python", VersionFile: ".python-version"}
	v, err := ReadPythonVersion(root, s)
	if err != nil {
		t.Fatal(err)
	}
	for file, want := range map[string]string{
		"app/x.py":            "",
		"lib/foo/foo/core.py": "lib/foo/.python-version pins 3.9",
		"lib/bar/bar.py":      "",
		"lib/env/e.py":        "lib/env/mise.staging.toml",
		"docs/conf.py":        "",
	} {
		got := PythonVersionConflict(root, s, v, file)
		if (want == "") != (got == "") || !strings.Contains(got, want) {
			t.Errorf("%s: conflict %q, want %q", file, got, want)
		}
	}
}
