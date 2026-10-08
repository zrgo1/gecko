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
	"github.com/zrgo/gecko/internal/prompt"
	"github.com/zrgo/gecko/internal/provider"
	"github.com/zrgo/gecko/internal/provider/openai"
	"github.com/zrgo/gecko/internal/runner"
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

	err := newRootCmd().ExecuteContext(ctx)
	if ctx.Err() != nil {
		fmt.Fprintln(os.Stderr, "gecko: interrupted")
	} else if err != nil {
		fmt.Fprintln(os.Stderr, "gecko:", err)
	}
	return runner.ExitCode(ctx, err)
}

// newRegistry lists every provider type gecko supports.
func newRegistry() *provider.Registry {
	r := provider.NewRegistry()
	r.Register(openai.NewOpenAI, "openai")
	r.Register(openai.NewCompatible, "openai-compatible")
	return r
}

func newRootCmd() *cobra.Command {
	return newRootCmdWithRegistry(newRegistry())
}

// newRootCmdWithRegistry lets integration tests supply an in-process provider.
func newRootCmdWithRegistry(registry *provider.Registry) *cobra.Command {
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
			return run(cmd, opts, inv, registry)
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

// run asks the provider for a command, then hands it to the confirmation runner.
func run(cmd *cobra.Command, opts *Options, inv Invocation, registry *provider.Registry) error {
	resolved, err := loadConfig(cmd, opts)
	if err != nil {
		return err
	}
	stderr := cmd.ErrOrStderr()
	if opts.Verbose {
		fmt.Fprintf(stderr, "gecko: %s\n", resolved)
	}

	p, err := registry.New(resolved.Type, provider.Config{
		Name:    resolved.ProviderName,
		Model:   resolved.Model,
		BaseURL: resolved.BaseURL,
		APIKey:  resolved.APIKey,
	})
	if err != nil {
		return err
	}

	shell := prompt.ShellPath(resolved.Shell, os.Getenv)
	req := prompt.Build(prompt.DetectDefault(shell), inv.Hint, inv.Query)
	if opts.Verbose {
		fmt.Fprintf(stderr, "gecko: system prompt:\n%s\ngecko: user prompt:\n%s\n", req.System, req.User)
	}

	s, err := p.Suggest(cmd.Context(), req)
	if err != nil {
		return err
	}

	r := runner.New(cmd.InOrStdin(), cmd.OutOrStdout(), stderr)
	return r.Run(cmd.Context(), s, shell, opts.DryRun, opts.Execute)
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
