package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	graphrepo "github.com/nonchan7720/depgraph/pkg/infrastructure/repository/graph"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/repository/hooksession"
	projectrepo "github.com/nonchan7720/depgraph/pkg/infrastructure/repository/project"
	rulesrepo "github.com/nonchan7720/depgraph/pkg/infrastructure/repository/rules"
	"github.com/nonchan7720/depgraph/pkg/interfaces/handler/hook"
)

func newHookCommand(o *options) *cobra.Command {
	c := &cobra.Command{
		Use:   "hook <session-start|prompt>",
		Short: "Claude Code hook: add findings to the prompt context (read-only, always exits 0)",
		Long: `Reads the Claude Code hook input from stdin and prints the findings already in
Neo4j as hookSpecificOutput.additionalContext. session-start prints a compact
summary, prompt prints only what changed since the last injection in the same
session (the state is kept in Neo4j with the project, pruned after 7 days and
removed with it). Nothing is printed
when Neo4j is unreachable, the root's project (same key rules as scan:
--project, .depgraph.yml, directory name, @worktree) is not scanned or its key
belongs to another root, or --timeout expires.
The root is --root, else $CLAUDE_PROJECT_DIR, else the cwd of the hook input.`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"session-start", "prompt"},
		// root の prepare（.depgraphignore 読込など）は hook には不要で、失敗の種にもなるので行わない。
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, args []string) error {
			ev, err := hook.ParseEvent(args[0])
			if err != nil {
				return err // 設定ミス（Claude Code 側では非ブロッキングのエラー扱い）
			}
			flagRoot := ""
			if cmd.Flags().Changed("root") {
				flagRoot = o.root
			}
			debugf := func(string, ...any) {}
			if o.hookDebug {
				debugf = func(format string, a ...any) {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "depgraph hook: "+format+"\n", a...)
				}
			}
			text := runHook(cmd.Context(), o, ev, cmd.InOrStdin(), flagRoot, debugf)
			_, _ = cmd.OutOrStdout().Write(hook.Output(ev, text))
			return nil
		},
	}
	addNeo4jFlags(c.Flags(), o)
	c.Flags().DurationVar(&o.hookTimeout, "timeout", 3*time.Second, "give up (print nothing) after this long")
	c.Flags().BoolVar(&o.hookDebug, "debug", false, "report why nothing was injected on stderr")
	return c
}

// runHook は注入する文脈を返す。失敗・タイムアウト・パニックはすべて "" にする
// （hook は絶対にプロンプトを止めない）。
func runHook(parent context.Context, o *options, ev hook.Event, stdin io.Reader, flagRoot string, debugf func(string, ...any)) string {
	ctx, cancel := context.WithTimeout(parent, o.hookTimeout)
	defer cancel()
	done := make(chan string, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				debugf("panic: %v", r)
				done <- ""
			}
		}()
		in, err := hook.ParseInput(stdin)
		if err != nil {
			debugf("%v", err)
			done <- ""
			return
		}
		root := hook.ResolveRoot(flagRoot, os.Getenv("CLAUDE_PROJECT_DIR"), in.Cwd)
		st, err := graphrepo.New(ctx, o.neo4jURI, o.neo4jUser, o.neo4jPass, o.neo4jDB)
		if err != nil {
			debugf("%v", err)
			done <- ""
			return
		}
		defer func() { _ = st.Close(context.WithoutCancel(ctx)) }()
		src := projectData{
			projects: projectrepo.New(st),
			flagKey:  o.project,
			scoped: func(key string) (statsReader, rules.Repository) {
				return graphrepo.ForProject(st, key), rulesrepo.New(st, key)
			},
			config: loadRules,
			sessions: func(key string) hook.SessionStore {
				return hooksession.New(st, key)
			},
		}
		text, err := hook.Run(ctx, ev, in, root, src, time.Now())
		if err != nil {
			debugf("%v", err)
		}
		if text == "" {
			debugf("nothing to inject for root %q", root)
		}
		done <- text
	}()
	select {
	case text := <-done:
		return text
	case <-ctx.Done():
		debugf("gave up: %v", ctx.Err())
		return ""
	}
}
