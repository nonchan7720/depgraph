# Coding agents: plugins, hooks and the quality gate

English | [日本語](ja/agents.md) · [README](../README.md)

depgraph feeds its findings back to the agent while it works, not only in review: a plugin for Claude Code and Codex, the hooks behind it, and a gate that keeps an agent from finishing while debt it added remains.

## Claude Code plugin

The repository is also a Claude Code plugin marketplace. The `depgraph` plugin wires the
[hooks](#claude-code-hooks) for you and adds two skills:

```text
/plugin marketplace add nonchan7720/depgraph
/plugin install depgraph@depgraph
```

| part | what it does |
|---|---|
| hooks | `depgraph hook session-start` / `prompt` at session start and on every prompt (as below). They do nothing without a depgraph binary (see below), when the project's `.claude/settings.json` runs `depgraph hook` itself, and (session start only) when claude-hook-gate's `session_start` runs it, so the context is never added twice |
| `/depgraph:setup [init\|update\|review]` | writes `.depgraph.yml` (layers from what the code shows, entry points, rule options), `.depgraphignore` and, with claude-hook-gate, the `depgraph gate` rule and `session_start` entry in `.claude/gate.yaml` |
| `/depgraph:fix [rule]` | fixes findings the way the code needs (move the import that points the wrong way, reuse instead of re-implement, wire up or remove unused code), not by silencing rules |

The plugin installs the binary of its own version (the release it was made with), like
claude-hook-gate does: the session-start hook downloads `depgraph_<os>_<arch>.tar.gz` and
`checksums.txt` from the GitHub release `v<version>`, checks the sha256 and extracts `depgraph` into
`${XDG_CACHE_HOME:-~/.cache}/depgraph/<version>/` (once per version; versions unused for 30 days are
removed). The prompt hook only uses a binary that is already there. The skills run it through
`plugin/scripts/depgraph`, a wrapper that installs it the same way. The binary is, in order:

1. `$DEPGRAPH_BIN` (for development, e.g. `bin/depgraph` of a checkout)
2. the cached binary (`$DEPGRAPH_CACHE_DIR` replaces the cache directory)
3. a download from the release (linux / darwin × amd64 / arm64; needs `curl` or `wget`, and
   `sha256sum` or `shasum`: nothing is installed without a verified checksum)
4. `depgraph` on `PATH` (offline, Windows)

When none works, the hooks do nothing and print one line on stderr with the reason. Keep
`depgraph watch --root <repo>` running in a terminal. It needs `depgraph` on `PATH` (see
[Install](install.md)), and so do the claude-hook-gate entries below.

## Codex plugin

The same `plugin/` directory is a [Codex](https://developers.openai.com/codex) plugin with the
same hooks and skills; `.agents/plugins/marketplace.json` lists it. Add the marketplace, then
install `depgraph` from `/plugins` in Codex:

```text
codex plugin marketplace add nonchan7720/depgraph
```

Not every Codex build runs the hooks a plugin bundles: the plugin installs, but no findings reach
the session. Copy the hooks into `~/.codex/hooks.json` once. This command also updates the plugin
first (`codex plugin marketplace upgrade depgraph`, `codex plugin add depgraph@depgraph`):

```text
curl -fsSL https://raw.githubusercontent.com/nonchan7720/depgraph/main/plugin/codex/setup.sh | bash
```

Then open `/hooks` in Codex and trust the hooks; Codex runs no hook you have not trusted. Codex
records trust by hash, so after updating the plugin run the command again (the copy points at the
version directory) and trust the hooks again.

| part | what it does |
|---|---|
| `plugin/codex/hooks.json` | the hooks: `scripts/hook.sh session-start codex` / `prompt codex`, the same `depgraph hook` as in Claude Code (Codex reads the same `hookSpecificOutput.additionalContext`) |
| `plugin/codex/setup.sh` | the command above: updates the plugin (`--no-update` skips it; `DEPGRAPH_CODEX_BIN` names `codex`), finds the newest version in `${CODEX_HOME:-~/.codex}/plugins/cache/depgraph/depgraph/` (`DEPGRAPH_PLUGIN_DIR` names another plugin directory) and runs its `install-hooks.sh` with the remaining arguments (`... \| bash -s -- --repo`); under a managed-hooks-only policy, or with `--system`, it writes `/etc/codex/hooks.json` instead (see below) |
| `plugin/codex/install-hooks.sh` | `depgraph codex install-hooks` with this plugin's binary: copies the hooks into `$CODEX_HOME/hooks.json` (default), `<repo>/.codex/hooks.json` (`--repo [--root <dir>]`, runs only in a project Codex trusts) or any file (`--to <file>`); `--print` prints instead of writing, `--remove` takes the hooks out. Other hooks in the file are kept, and running it again replaces its own entries |
| skills | `setup` and `fix`, as in Claude Code (Codex has no `CLAUDE_PLUGIN_ROOT`: the skills find the wrapper from their own path) |

- Root: Codex sets no project directory, so the root is the session's `cwd`. Start Codex in the
  directory `depgraph watch --root` serves.
- No double context: Codex runs every hook it finds. The plugin's own hooks stay quiet while a copy
  whose script exists is in `~/.codex/hooks.json`, `<repo>/.codex/hooks.json` or
  `/etc/codex/hooks.json` (the copy does the work, also in builds that skip plugin hooks). Both stay
  quiet when a Codex `hooks.json` / `config.toml` of the project, the user or the system runs
  `depgraph hook` itself, and session start does when claude-hook-gate's `session_start` runs it
  (`.agents/gate.yaml` or `.claude/gate.yaml`).
- Managed hooks only (`allow_managed_hooks_only = true` in the administrator's
  `requirements.toml`): Codex skips user, project and plugin hooks, but runs the hooks of the system
  layer, `/etc/codex/hooks.json`, without a trust prompt. The same `setup.sh` command handles it: when
  `/etc/codex/requirements.toml` sets the policy, it installs the hooks into `/etc/codex/hooks.json`
  instead (the merged file is made as you; only putting it in place runs `sudo`, which asks for your
  password) and removes the copy from `~/.codex/hooks.json`, so one copy runs. When the policy comes
  from somewhere it cannot see (an MDM profile, the ChatGPT workspace), pass `--system`
  (`... | bash -s -- --system`). `--remove` takes the hooks out of the system file again. Without
  administrator rights it prints the command to hand to an administrator. The hooks point at the plugin
  in your `~/.codex`, so they run for you; other users of the machine run their own `setup.sh`

- The quality gate below works the same under Codex with claude-hook-gate's Codex plugin, which
  reads `.agents/gate.yaml` first, then `.claude/gate.yaml`.

## Claude Code hooks

`depgraph hook <session-start|prompt>` feeds the findings already in Neo4j back into
Claude Code (read-only: it never scans and never writes CLAUDE.md).

- `session-start`: a compact summary — stats, then findings by severity (errors first);
  rules with many findings show the count and a few examples, the whole output is capped
  (about 4,000 characters) so even 50 `duplicate-symbol` findings do not flood the context.
  It also records the session's baseline (the findings it started with; kept on resume and compact)
  for [`depgraph gate`](#quality-gate-claude-hook-gate)
- `prompt`: only the findings that appeared or were resolved since the last injection in the
  same session; nothing when nothing changed. The state is kept per `session_id` in
  Neo4j next to the project's graph (`HookSession` nodes): the 20 newest sessions of a project, none
  older than 7 days, removed with the project (`projects rm`, `--reset`). Nothing is written into the
  repository (the state file of earlier versions, `.claude/.depgraph-hook-state.json`, is deleted)
- Root: `--root`, else `$CLAUDE_PROJECT_DIR`, else `cwd` of the hook input
- The project is resolved like `scan` does (`--project`, `.depgraph.yml`, directory name,
  `@<worktree>`); only that project's findings are injected
- It never blocks a prompt: when Neo4j is unreachable, the root's project has not been scanned
  (or its key belongs to another live root), or `--timeout` (default `3s`) expires, it prints nothing and exits 0
  (`--debug` tells why on stderr). Output is `hookSpecificOutput.additionalContext` JSON

To use it in another repository, keep `depgraph watch --root <repo>` running and add this to
that repository's `.claude/settings.json` (this repository uses `bin/depgraph` the same way):

```json
{
  "hooks": {
    "SessionStart": [
      { "hooks": [ { "type": "command", "timeout": 10,
        "command": "command -v depgraph >/dev/null && depgraph hook session-start; exit 0" } ] }
    ],
    "UserPromptSubmit": [
      { "hooks": [ { "type": "command", "timeout": 10,
        "command": "command -v depgraph >/dev/null && depgraph hook prompt; exit 0" } ] }
    ]
  }
}
```

The hook uses the same Neo4j as the other commands (see [Neo4j](neo4j.md)): an external one
when `--neo4j` / `$NEO4J_URI` is set, else the running depgraph container of the root, found by
its labels. It never starts a container; with none running it prints nothing.

### Quality gate (claude-hook-gate)

`prompt` only runs when the user sends a prompt: during one long agent turn that rewrites many
files, nothing reaches the agent. `depgraph gate` closes that gap as a check of
[claude-hook-gate](https://github.com/nonchan7720/claude-hook-gate) (`.claude/gate.yaml`),
which runs commands after each edit and before the agent stops:

```yaml
rules:
  # depgraph: debt this session added (needs `depgraph watch`; passes without it)
  - match: ["**/*.go", "**/*.{ts,tsx,js,jsx,mjs,cjs}", "**/*.py"]
    timeout: 60
    run:
      - { name: depgraph, cmd: "command -v depgraph >/dev/null || exit 0; depgraph gate" }
```

- It applies the changed files (`$CLAUDE_GATE_FILES`, or the arguments) to the graph itself, as
  `watch` does, so it does not race `watch`'s debounce, then evaluates the rules and lists the findings
  of at least `--fail-on` (default `warn`) that the session (`$CLAUDE_SESSION_ID`) **added**
  since its baseline. With any it exits 1: the gate hands the list to the agent, and because a failing
  rule is rerun when the agent stops, **the agent cannot finish while the debt it added remains**
  (claude-hook-gate gives up after 5 blocked stops). Debt that was there when the session started never fails it.
- The baseline is recorded by `depgraph hook session-start`. Without one, the findings before the first
  gate run's changes are the baseline. With a claude-hook-gate that has the `session_start` phase, the
  whole integration lives in `gate.yaml` (instead of the `SessionStart` hook in `settings.json`):

  ```yaml
  session_start:
    - { name: depgraph, timeout: 10, cmd: "command -v depgraph >/dev/null || exit 0; depgraph hook session-start --format text" }
  ```

  `--format text` prints the summary itself (the gate injects stdout as is); the session comes from
  `$CLAUDE_SESSION_ID` when the hook input has none.
- `--fail-on error` fails only on new import cycles and layer violations; the other findings then
  still reach the agent through the `prompt` hook.
- It passes (exit 0, the reason on stderr) whenever it cannot tell: no running store (it never starts
  Neo4j), the root's project not scanned, no session, or `--timeout` (default `60s`) expired.
  It never writes CLAUDE.md. The root is `--root`, else the current directory's git worktree when it is another worktree of
  `$CLAUDE_PROJECT_DIR`'s repository (see below), else `$CLAUDE_PROJECT_DIR`, else the current directory.
- **Agents in git worktrees.** claude-hook-gate runs the rules of a file under `.claude/worktrees/<name>/`
  in that worktree, with `$CLAUDE_PROJECT_DIR` still the main tree and `$CLAUDE_GATE_FILES` relative to the
  worktree. The gate then checks the worktree, not the main tree, as its own project (`<base>@<name>`, see
  [Projects](neo4j.md#projects)) in the Neo4j of the main tree's `watch`, so one `depgraph watch` of the main tree is
  enough. Findings of the worktree reach only the agent working there; the main tree's project is not touched.
  - The worktree's graph is built from its base, the newest commit on its history it did not make itself (by
    its `HEAD` reflog; the exact committed tree, plus ignored files and submodules present in the checkout),
    and that is the session's baseline: what the agent changed or committed before the first run still counts
    as added, debt that was there before does not.
  - Every run then applies what git says may differ from the graph, besides `$CLAUDE_GATE_FILES`: files changed
    between the recorded commit and `HEAD` (commits, `git reset` / `rebase`) and uncommitted or untracked files
    whose content changed since they were applied. A file changed only by commits the worktree did not make
    (another agent's work merged or rebased in from upstream) is upstream: the debt it brings is added to the
    baselines of the worktree's sessions, as it is not this agent's. Everything else, including what the agent
    edited and committed in one step, counts as the agent's. When the worktree's base moves (a rebase or
    `pull --rebase` onto upstream), the findings of the new base are added to the baselines too, so debt that
    came with main is never the agent's, even in a file its replayed commit touched. A merge commit, conflict
    resolution included, is upstream (a conflicted file holds the merged branch's changes too; debt added only in a resolution is not
    reported). While a merge, cherry-pick, revert or
    rebase is in progress, the gate passes and says so: the incoming changes are staged as if the agent made
    them; it checks once the operation is committed.
  - The first build, absorbing upstream changes, and a delta that touches a file import resolution depends on
    (`go.mod`, `tsconfig.json`, `package.json`, ...) or more than 200 files, run in a background `depgraph` process under a lease on the
    project, so a large repository is never stuck behind the gate's `--timeout`. The gate waits for it as long
    as its timeout allows and otherwise passes, saying so on stderr; the next run checks.
  - Projects of removed worktrees of the same main tree are deleted after a build (`depgraph projects prune`
    deletes them too).
