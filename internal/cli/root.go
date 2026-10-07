// Package cli wires the gecko command line.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"

	"github.com/spf13/cobra"

	"github.com/zrgo/gecko/internal/config"
	"github.com/zrgo/gecko/internal/provider"
	"github.com/zrgo/gecko/internal/provider/openai"
)

// version is overridden at build time: -ldflags "-X github.com/zrgo/gecko/internal/cli.version=v0.1.0"
var version = "dev"

// Options holds flag values. Empty strings mean "not set" so config loading
// can fill them in later (flags > env > config file).
type Options struct {
	Execute  bool
	DryRun   bool
	Verbose  bool
	Provider string
	Model    string
	BaseURL  string
	Shell    string
}

// Execute runs the root command and returns the process exit code.
func Execute() int {
	// Ctrl-C cancels an in-flight API request instead of killing the process mid-write.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := newRootCmd().ExecuteContext(ctx); err != nil {
		if ctx.Err() != nil {
			fmt.Fprintln(os.Stderr, "gecko: interrupted")
			return 130
		}
		fmt.Fprintln(os.Stderr, "gecko:", err)
		return 1
	}
	return 0
}

// newRegistry lists every provider type gecko supports.
func newRegistry() *provider.Registry {
	r := provider.NewRegistry()
	r.Register(openai.NewOpenAI, "openai")
	r.Register(openai.NewCompatible, "openai-compatible")
	return r
}

func newRootCmd() *cobra.Command {
	opts := &Options{}

	cmd := &cobra.Command{
		Use:   "gecko [tool-hint] <query>",
		Short: "Turn a plain-English request into a shell command",
		Long: `gecko asks an LLM for a shell command that does what you describe,
shows it to you, and runs it after you confirm.

The optional first word is a tool hint: if it names a program on your PATH
(grep, find, git, ...), gecko asks the model to prefer that tool.`,
		Example: `  gecko grep "files starting with t in the current directory"
  gecko "show disk usage by folder, largest first"
  gecko -x find "empty directories under src"
  gecko -p groq -m llama-3.3-70b-versatile "list listening ports"`,
		Args:          cobra.MinimumNArgs(1),
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			inv, err := parseArgs(args, exec.LookPath)
			if err != nil {
				return err
			}
			return run(cmd, opts, inv)
		},
	}

	f := cmd.Flags()
	f.BoolVarP(&opts.Execute, "execute", "x", false, "run the suggested command immediately, without confirmation")
	f.BoolVar(&opts.DryRun, "dry-run", false, "print the suggested command and exit, never run it")
	f.BoolVarP(&opts.Verbose, "verbose", "v", false, "print debug info (resolved config, prompt, raw response)")
	f.StringVarP(&opts.Provider, "provider", "p", "", "provider profile from config (default: default_provider)")
	f.StringVarP(&opts.Model, "model", "m", "", "model name, overrides the profile")
	f.StringVar(&opts.BaseURL, "base-url", "", "API base URL, overrides the profile")
	f.StringVar(&opts.Shell, "shell", "", "shell used to run the command (default: $SHELL)")
	cmd.MarkFlagsMutuallyExclusive("execute", "dry-run")

	return cmd
}

// run asks the provider for a command and prints it. Execution and the
// confirm prompt arrive with the executor task.
func run(cmd *cobra.Command, opts *Options, inv Invocation) error {
	resolved, err := loadConfig(cmd, opts)
	if err != nil {
		return err
	}
	stderr := cmd.ErrOrStderr()
	if opts.Verbose {
		fmt.Fprintf(stderr, "gecko: %s\n", resolved)
	}

	p, err := newRegistry().New(resolved.Type, provider.Config{
		Name:    resolved.ProviderName,
		Model:   resolved.Model,
		BaseURL: resolved.BaseURL,
		APIKey:  resolved.APIKey,
	})
	if err != nil {
		return err
	}

	req := tempRequest(inv)
	if opts.Verbose {
		fmt.Fprintf(stderr, "gecko: system prompt:\n%s\ngecko: user prompt:\n%s\n", req.System, req.User)
	}

	s, err := p.Suggest(cmd.Context(), req)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "command:     %s\n", s.Command)
	fmt.Fprintf(out, "explanation: %s\n", s.Explanation)
	fmt.Fprintf(out, "risk:        %s\n", s.Risk)
	fmt.Fprintln(stderr, "gecko: not executed (executor not wired up yet)")
	return nil
}

func loadConfig(cmd *cobra.Command, opts *Options) (config.Resolved, error) {
	path, err := config.Path(os.Getenv)
	if err != nil {
		return config.Resolved{}, err
	}
	file, found, err := config.Load(path)
	if err != nil {
		return config.Resolved{}, err
	}
	if opts.Verbose {
		if found {
			fmt.Fprintf(cmd.ErrOrStderr(), "gecko: using config %s\n", path)
		} else {
			fmt.Fprintf(cmd.ErrOrStderr(), "gecko: no config at %s, using built-in defaults\n", path)
		}
	}
	return config.Resolve(file, os.Getenv, config.Overrides{
		Provider: opts.Provider,
		Model:    opts.Model,
		BaseURL:  opts.BaseURL,
		Shell:    opts.Shell,
	})
}
