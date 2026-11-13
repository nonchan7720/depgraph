---
name: validate-rules
description: The real open-source repositories depgraph's rules are validated on, per language - Rails (mastodon, discourse, lobsters), Python / Django (django, saleor), Go (kubernetes) - and the command that scans them. Use when adding or changing a rule, an extraction of the resolver or import resolution, before opening the PR for it.
argument-hint: "[rule...] [rails|python|go|<repository>...]"
---

# Repositories for validating rules

Arguments: `$ARGUMENTS` (the rules under review, and optionally the groups or repositories to
scan; default: every repository).

Validate on these repositories, the same ones every time, so results are comparable between PRs:

| group | repository | what it covers |
|---|---|---|
| `rails` | mastodon/mastodon | Rails application, large TypeScript / JavaScript front end |
| `rails` | discourse/discourse | Rails application with plugins, large JavaScript (Ember) front end |
| `rails` | lobsters/lobsters | small classic Rails application |
| `python` | django/django | the Django framework itself |
| `python` | saleor/saleor | a large Django application |
| `go` | kubernetes/kubernetes | a large Go repository (many packages, nested modules) |

- gitlabhq/gitlabhq is left out on purpose: too large to scan in a review loop.
- Only public repositories belong here (this repository is public). The list lives in
  `scripts/validate-repos.sh` (`CORPUS`); change it there and in this table together.

## Scanning them

```bash
mise run build
scripts/validate-repos.sh --rule <rule> [--rule <rule>...] [rails|python|go|<name>...]
```

It clones each repository once (shallow, into `${XDG_CACHE_HOME:-~/.cache}/depgraph/validate`),
scans it as a throwaway project `zz-validate-<name>` in the Neo4j `bin/depgraph` uses, prints the
findings per rule and the findings of the rules under review, and removes the project.
`--counts FILE` writes `repo / rule / count` lines: run it with the base branch's binary
(`--bin`) and with this one, and `diff` the two files to see what else changed.

## What to do with the result

- Open the files of the reported findings (all of them, or at least 10 per repository) and decide:
  true debt, true but by design, or wrong. Wrong is a bug: regression test first, then the fix. A
  pattern that is by design is narrowed out of the rule or the severity lowered; a rule that
  fires everywhere reports a habit, not debt.
- Zero findings prove nothing: plant 2-5 cases of the debt in a clone, check they are reported,
  then restore the clone (`git checkout . && git clean -fd`).
- Put the table (repository / findings / what they were) and the count differences of the other
  rules in the PR body. Never commit a clone, a finding list or code copied from a repository.
