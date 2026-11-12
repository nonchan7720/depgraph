---
name: validate-rules
description: Judge a new or changed depgraph rule (or resolver change) on real open-source repositories before it ships - Rails (mastodon, discourse, lobsters), Django (django, saleor), Go (kubernetes). Use when adding or changing a rule, an extraction of the resolver, or import resolution, and before opening the PR for it.
argument-hint: "[rule...] [rails|python|go|<repository>...]"
---

# Validating a rule on real repositories

Arguments: `$ARGUMENTS` (the rules under review, and optionally the groups or repositories to
scan; default: every repository).

A finding that is not true teaches people to ignore depgraph, so a rule ships only after it was
read against real code. Unit tests show the rule does what was intended; this shows whether what
was intended is debt.

## Corpus

`scripts/validate-repos.sh` holds the list. Keep using the same repositories, so results stay
comparable between PRs:

| group | repositories |
|---|---|
| `rails` | mastodon/mastodon, discourse/discourse, lobsters/lobsters |
| `python` | django/django (the framework), saleor/saleor (a Django application) |
| `go` | kubernetes/kubernetes |

mastodon and discourse also carry a large TypeScript / JavaScript front end, so they cover
those rules too. gitlabhq is left out on purpose: it is too large to scan in a review loop.
Only public repositories belong in the list (this repository is public).

## Steps

1. **Neo4j.** The script uses whatever `bin/depgraph` uses: `NEO4J_URI` (in this repository
   `mise run neo4j` starts the compose one), else its own container. Every repository is its own
   project `zz-validate-<name>`, removed after the scan.
2. **Baseline.** Build the base of the PR (`main`, or the branch below in a stack) and record its
   counts:

   ```bash
   git stash -u; git checkout <base>; mise run build; cp bin/depgraph /tmp/depgraph-base
   git checkout -; git stash pop; mise run build
   scripts/validate-repos.sh --bin /tmp/depgraph-base --counts /tmp/base.tsv
   ```
3. **This change.**

   ```bash
   scripts/validate-repos.sh --counts /tmp/head.tsv --rule <rule> [--rule <rule>...] [--limit 40]
   diff /tmp/base.tsv /tmp/head.tsv
   ```

   - Every difference in a rule the change did not mean to touch is a regression: explain it or fix it.
   - A scan that fails or gets much slower is a finding too.
4. **Read the findings of the rule under review.** Per repository, open the files of at least
   10 findings (all of them when there are fewer) and decide for each: true and worth fixing /
   true but by design / wrong. Clones are in `${XDG_CACHE_HOME:-~/.cache}/depgraph/validate/<name>`.
   - *Wrong*: a bug. Add a regression test with the smallest code that shows it, fix it, run again.
   - *True but by design* (a recursive descent parser split over files, entities that refer to
     each other): narrow the rule until it no longer reports the pattern, or lower its severity.
     Do not ship it and tell people to ignore it.
   - Many findings in every repository mean the rule reports a habit, not debt: reconsider it.
5. **Missed findings.** Plant the debt the rule is for in one clone (2-5 cases, in different
   shapes), scan again and check each is reported; then `git -C <clone> checkout . && git -C
   <clone> clean -fd`.
6. **Report** in the PR body: a table of repository / findings of the rule / what they were, the
   false positives found and fixed, what is knowingly not reported, and the count differences of
   the other rules (or "none").

## Rules

- Never commit a clone, a finding list of a repository, or code copied from one; describe the
  pattern in your own minimal example for the regression test.
- Do not tune thresholds until the corpus is quiet. Zero findings prove nothing: check step 5.
