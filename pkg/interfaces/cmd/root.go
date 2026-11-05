// Package cmd is the depgraph command line (cobra).
package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/nonchan7720/depgraph/pkg/infrastructure/neo4jprovider/container"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver"
	"github.com/nonchan7720/depgraph/pkg/version"
)

// options holds every flag value; each subcommand registers the subset it uses.
type options struct {
	root       string
	project    string
	neo4jURI   string
	neo4jUser  string
	neo4jPass  string
	neo4jDB    string
	claudeMD   string
	reset      bool
	debounce   time.Duration
	addr       string
	noUI       bool
	noLSP      bool
	exclude    string
	ignoreFile string
	workers    int

	hookTimeout time.Duration
	hookDebug   bool
	hookFormat  string
	gateTimeout time.Duration

	// how Neo4j is provided (see neo4j.go)
	neo4jProvider string
	containerCLI  string
	persist       bool
	persistSet    bool // --persist was given (it then beats .depgraph.yml)
	// containerConfig is the base of the container provider (tests replace
	// the runner / PATH lookup).
	containerConfig container.ProviderConfig
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Execute runs the CLI; SIGINT / SIGTERM cancel the command's context.
func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := newRootCommand(&options{}).ExecuteContext(ctx)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "depgraph:", err)
		os.Exit(1)
	}
}

func newRootCommand(o *options) *cobra.Command {
	root := &cobra.Command{
		Use:   "depgraph",
		Short: "Multi-language dependency graph for AI-written code, backed by Neo4j",
		Long: `depgraph parses Go / TypeScript / JavaScript / Python repositories with
tree-sitter (optionally refined by language servers), stores the dependency
graph in Neo4j, checks architectural rules and reports them to CLAUDE.md and
a browser UI.`,
		Version:       version.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			o.persistSet = cmd.Flags().Changed("persist")
			return o.prepare()
		},
	}
	root.SetVersionTemplate(versionTemplate())
	pf := root.PersistentFlags()
	pf.StringVar(&o.root, "root", ".", "repository root")
	pf.StringVar(&o.exclude, "exclude", "", "comma-separated exclude patterns (name glob, or repo-relative path/glob)")
	pf.StringVar(&o.project, "project", "", "project key in the shared database (default: \"project\" in <root>/.depgraph.yml, else the root's directory name; a linked git worktree adds @<name>)")
	pf.StringVar(&o.ignoreFile, "ignore-file", ".depgraphignore", "ignore file with one pattern per line, relative to --root (absolute path allowed, empty to disable)")

	root.AddCommand(
		newParseCommand(o),
		newScanCommand(o),
		newCheckCommand(o),
		newWatchCommand(o),
		newServeCommand(o),
		newMCPCommand(o),
		newHookCommand(o),
		newGateCommand(o),
		newGateSyncCommand(o),
		newProjectsCommand(o),
		newStoreCommand(o),
		newSchemaCommand(),
		newContainerWatchdogCommand(),
		newVersionCommand(),
	)
	return root
}

// prepare makes root absolute and registers the ignore file and --exclude patterns.
func (o *options) prepare() error {
	abs, err := filepath.Abs(o.root)
	if err != nil {
		return err
	}
	o.root = abs
	// .depgraphignore patterns are repo-relative, so the file is looked up
	// under --root (not the current directory) unless an absolute path is given.
	if err := resolver.LoadIgnoreFile(resolver.IgnoreFilePath(o.root, o.ignoreFile)); err != nil {
		return err
	}
	if o.exclude != "" {
		resolver.AddSkipPatterns(strings.Split(o.exclude, ","))
	}
	return nil
}

// ---- shared flag groups ----------------------------------------------------

// addNeo4jFlags registers how Neo4j is reached. Without --neo4j / NEO4J_URI
// depgraph provides Neo4j itself (see neo4j.go and --neo4j-provider).
func addNeo4jFlags(fs *pflag.FlagSet, o *options) {
	fs.StringVar(&o.neo4jURI, "neo4j", env("NEO4J_URI", ""), "bolt URI of an external Neo4j ($NEO4J_URI); unset: depgraph starts and cleans up its own Neo4j (see --neo4j-provider)")
	fs.StringVar(&o.neo4jUser, "user", env("NEO4J_USER", "neo4j"), "external Neo4j user ($NEO4J_USER)")
	fs.StringVar(&o.neo4jPass, "pass", env("NEO4J_PASSWORD", "depgraph123"), "external Neo4j password ($NEO4J_PASSWORD)")
	fs.StringVar(&o.neo4jDB, "db", env("NEO4J_DATABASE", "neo4j"), "external Neo4j database ($NEO4J_DATABASE)")
	fs.StringVar(&o.neo4jProvider, "neo4j-provider", env("DEPGRAPH_NEO4J_PROVIDER", ""), "how Neo4j is provided: external, container (local is planned); default: external when a URI is set, else container ($DEPGRAPH_NEO4J_PROVIDER, store.provider in .depgraph.yml)")
	fs.StringVar(&o.containerCLI, "container-cli", env("DEPGRAPH_CONTAINER_CLI", ""), "container CLI of the container provider: docker, podman, nerdctl or a path; default: the first of them that answers ($DEPGRAPH_CONTAINER_CLI, store.containerCli in .depgraph.yml)")
}

// addPersistFlag is for the commands that may start a store.
func addPersistFlag(fs *pflag.FlagSet, o *options) {
	fs.BoolVar(&o.persist, "persist", false, "container provider: keep the data in a named volume (the container is stopped on exit and started again next time; default: store.persist in .depgraph.yml, else ephemeral; delete with `depgraph store rm`)")
}

func addClaudeMDFlag(fs *pflag.FlagSet, o *options) {
	fs.StringVar(&o.claudeMD, "claude-md", env("DEPGRAPH_CLAUDE_MD", "CLAUDE.md"), "file whose managed section is updated with findings, relative to --root ('' to disable, $DEPGRAPH_CLAUDE_MD)")
}

func addResetFlag(fs *pflag.FlagSet, o *options) {
	fs.BoolVar(&o.reset, "reset", false, "clear this project's data before scanning (other projects are kept)")
}

func addWorkersFlag(fs *pflag.FlagSet, o *options) {
	fs.IntVar(&o.workers, "workers", 0, "files parsed in parallel during a full scan (0: GOMAXPROCS)")
}

func addAddrFlag(fs *pflag.FlagSet, o *options) {
	fs.StringVar(&o.addr, "addr", env("DEPGRAPH_ADDR", "127.0.0.1:7777"), "address for the browser UI ($DEPGRAPH_ADDR)")
}
