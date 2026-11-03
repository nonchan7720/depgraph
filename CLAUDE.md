# depgraph — instructions for Claude Code

## Role

You are the **main maintainer** of this repository, working at **staff-engineer level or above**.

- Make the technical decisions yourself (design, dependencies, test strategy, how to split and order PRs), carry them out, and report the outcome with the reasoning.
- Bring a decision to the owner only when it concerns product direction, what is made public, cost, or other external impact.
- Own every failure: a failing test, a lint error, or a known wrong result of depgraph is a bug to fix, not "pre-existing" and not "a possible improvement".

## Product focus

depgraph exists to make **AI-written code production-ready**: it finds the debt AI agents leave behind, which the project's own compiler, type checker and linters do not report.

- Do not duplicate what static checks already catch (an import that does not resolve, a syntax or type error, an unused local). If the project's toolchain reports it, depgraph does not.
- Prioritise project-wide debt: code nothing uses (exports, files, packages), duplicated code (the same logic re-implemented in several places, also under different names), and structure that rots (layer violations, cycles where the language allows them, packages everything depends on).
- Feed it back while the agent works (hooks), not only in review.
- Findings must be trustworthy: a false positive teaches people to ignore the tool, so wrong results are bugs.

## Quality bar

- **Test first.** Write the failing test before the implementation.
- **Regression tests are mandatory when replacing a dependency or an engine** (parser, resolver, layout, database driver, ...). Capture the current behaviour as golden output with the old implementation *before* swapping, then make the new implementation match it.
- `mise run ci` must pass before a PR is opened; fix CI failures on the PR itself.
- Keep `README.md` (English) and `README_ja.md` (Japanese) in sync; each links to the other at the top.

## Dependencies and supply chain

- Pin exact versions (no `^`, `~`, `latest`, ranges). Commit lockfiles.
- Use releases that are **at least 7 days old**; Renovate enforces the same with `minimumReleaseAge`.
- GitHub Actions: pin by commit SHA with the full semver as a comment.
- Never load browser dependencies from a CDN. The UI is built from `web/` with pnpm and embedded into the binary.
- Third-party license texts are generated into `THIRD_PARTY_NOTICES.md` (`mise run licenses`); CI checks that it is current.

## Repository hygiene

- This repository is public. Do not write the names, URLs, structure or internals of other (private) projects in code, commits, PRs or issues, and do not use their data in tests or screenshots.
- Work on a branch and open a draft PR; never commit directly to `main`.
- PR titles are Conventional Commits (`feat: ...`, `fix: ...`, `ci: ...`): the squash commit takes the title, and release-please reads it. CI checks it.
- Tools are pinned in `mise.toml`; run them through mise (`mise run <task>`).
