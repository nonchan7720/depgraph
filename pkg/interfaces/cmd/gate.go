package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	"github.com/nonchan7720/depgraph/pkg/domain/view"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/repository/hooksession"
	projectrepo "github.com/nonchan7720/depgraph/pkg/infrastructure/repository/project"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver/treesitter"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/workspace"
	"github.com/nonchan7720/depgraph/pkg/interfaces/handler/hook"
)

// gateFailure is returned when the session added findings: the command exits
// non-zero, which the quality gate reports to the agent (and, at stop, blocks).
type gateFailure struct{ n int }

func (e *gateFailure) Error() string {
	return fmt.Sprintf("gate: %d new finding(s) in this session", e.n)
}

// splitGateFiles splits $CLAUDE_GATE_FILES: shell words separated by blanks,
// with '...' and "..." quoting and backslash escapes.
func splitGateFiles(s string) []string {
	var out []string
	var cur strings.Builder
	inWord, quote := false, rune(0)
	esc := false
	for _, r := range s {
		switch {
		case esc:
			cur.WriteRune(r)
			esc = false
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\\':
			esc, inWord = true, true
		case quote == '"':
			if r == '"' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == ' ' || r == '\t' || r == '\n':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out
}

// notifyFunc adapts a function to analysis.Notifier.
type notifyFunc func(view.Stats, []rules.Finding)

func (f notifyFunc) SetFindings(s view.Stats, fs []rules.Finding) { f(s, fs) }

// errGateSkip marks why the gate could not check (it then passes).
var errGateSkip = errors.New("skipped")

// runGateCheck applies files to the graph of o.root's project and returns the
// findings at least floor added in session. It never starts Neo4j and never
// writes CLAUDE.md; when the store or the project is not there, or the
// session is unknown, the error wraps errGateSkip. A linked git worktree is
// checked as its own project, which the gate builds and keeps up to date
// itself (see gate_worktree.go).
func runGateCheck(parent context.Context, o *options, session string, files []string, floor rules.Severity) ([]rules.Finding, error) {
	if session == "" {
		return nil, fmt.Errorf("%w: no CLAUDE_SESSION_ID", errGateSkip)
	}
	ctx, cancel := context.WithTimeout(parent, o.gateTimeout)
	defer cancel()
	here := workspace.Locate(ctx, o.root)
	worktree := here.Linked && here.Top == evalSymlinks(o.root)
	var alt []string // a worktree uses the store of its main tree
	if worktree {
		alt = append(alt, here.Main, os.Getenv("CLAUDE_PROJECT_DIR"))
	}
	st, live, err := hookStoreFor(ctx, o, o.root, alt...) // never starts Neo4j
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errGateSkip, err)
	}
	defer func() { _ = st.Close(context.WithoutCancel(ctx)) }()
	p, stored, err := resolveProject(ctx, projectrepo.New(st), o.root, o.project)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errGateSkip, err)
	}
	files = gateFiles(o.root, files)
	var a *app
	var plan *syncPlan
	if worktree {
		if err := st.EnsureSchema(ctx); err != nil {
			return nil, err
		}
		r := treesitter.New()
		w := &worktreeGate{o: o, st: st, projects: projectrepo.New(st), session: session, isConfig: r.IsConfigFile}
		wa, wp, err := w.prepare(ctx, p, stored, live)
		r.Close()
		if err != nil {
			return nil, err
		}
		a, plan = wa, &wp
		files = append(files, wp.own...)
	} else {
		if !stored {
			return nil, fmt.Errorf("%w: %s is not scanned (run depgraph watch)", errGateSkip, o.root)
		}
		if a, err = buildApp(ctx, st, o, nil, io.Discard); err != nil {
			return nil, err
		}
	}
	defer a.resolver.Close()

	var last []rules.Finding
	fresh := false // last is the evaluation of the current graph
	a.svc.SetNotifier(notifyFunc(func(_ view.Stats, fs []rules.Finding) { last, fresh = fs, true }))
	d := hook.GateDeps{
		Sessions: hooksession.New(st, a.project.Key),
		Apply: func(ctx context.Context, files []string) error {
			var rels []string
			for _, rel := range files {
				if !resolver.ShouldSkipPath(rel) && !slices.Contains(rels, rel) {
					rels = append(rels, rel)
				}
			}
			config, sources := splitChanges(rels, a.resolver.IsConfigFile, a.resolver.Supports)
			switch {
			case len(config) > 0: // import resolution changed: as watch does
				fresh = false
				return a.fullScan(ctx)
			case len(sources) > 0:
				fresh = false
				return a.svc.HandleChanges(ctx, sources) // evaluates
			}
			return nil
		},
		Evaluate: func(ctx context.Context) ([]rules.Finding, error) {
			if !fresh {
				if err := a.svc.Evaluate(ctx); err != nil {
					return nil, err
				}
			}
			fresh = false
			return last, nil
		},
	}
	added, err := hook.RunGate(ctx, d, session, files, floor, time.Now())
	if err == nil && plan != nil { // the graph now matches HEAD plus these
		a.project.Commit, a.project.Dirty = plan.head, plan.dirty
		err = a.projects.Upsert(ctx, a.project)
	}
	return added, err
}
func newGateCommand(o *options) *cobra.Command {
	var failOn string
	c := &cobra.Command{
		Use:   "gate [files...]",
		Short: "Quality-gate check for Claude Code: fail on debt added in this session",
		Long: `For claude-hook-gate (.claude/gate.yaml): applies the changed files
(the arguments, else $CLAUDE_GATE_FILES) to the graph of the root's project
and lists the findings of at least --fail-on that the session
($CLAUDE_SESSION_ID) added since it started; with any, it exits 1. The
session's baseline is recorded by "depgraph hook session-start"; without it,
the findings before the first gate run's changes are the baseline.
It never starts Neo4j and never writes CLAUDE.md: without a running store,
a scanned project or a session it passes (exit 0, the reason on stderr).
The root is --root, else the current directory's working tree when it is
another git worktree of $CLAUDE_PROJECT_DIR's repository, else
$CLAUDE_PROJECT_DIR, else the current directory. A linked worktree is
checked as its own project (<base>@<name>) in the Neo4j of its main tree:
the gate builds it from the worktree's HEAD the first time (in the
background when that takes long) and brings it up to date from git on
every run, so only "watch" of the main tree is needed. Debt that arrives
with commits the session did not make (a merge or rebase from upstream) is
not counted as the session's.`,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if !cmd.Flags().Changed("root") {
				if cwd, err := os.Getwd(); err == nil {
					o.root = gateRoot(cmd.Context(), os.Getenv("CLAUDE_PROJECT_DIR"), cwd)
				} else if d := os.Getenv("CLAUDE_PROJECT_DIR"); d != "" {
					o.root = d
				}
			}
			return o.prepare()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			floor := rules.Severity(failOn)
			if !floor.Valid() {
				return fmt.Errorf("--fail-on %q: want error, warn or info", failOn)
			}
			files := args
			if len(files) == 0 {
				files = splitGateFiles(os.Getenv("CLAUDE_GATE_FILES"))
			}
			added, err := runGateCheck(cmd.Context(), o, os.Getenv("CLAUDE_SESSION_ID"), files, floor)
			if err != nil {
				// the environment, not the code: never block the agent on it
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "depgraph gate: %v\n", err)
				if !errors.Is(err, errGateSkip) {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "depgraph gate: skipped")
				}
				return nil
			}
			if len(added) == 0 {
				return nil
			}
			_, _ = fmt.Fprint(cmd.OutOrStdout(), hook.RenderGate(added))
			return &gateFailure{n: len(added)}
		},
	}
	addNeo4jFlags(c.Flags(), o)
	c.Flags().StringVar(&failOn, "fail-on", string(rules.SeverityWarn), "fail on new findings of at least this severity (error, warn, info)")
	c.Flags().DurationVar(&o.gateTimeout, "timeout", 60*time.Second, "give up (pass) after this long")
	return c
}
