package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zrgo/gecko/internal/provider"
	"github.com/zrgo/gecko/internal/provider/mock"
	"github.com/zrgo/gecko/internal/runner"
)

func TestRunnerIntegration(t *testing.T) {
	// An absent temp config and in-process provider keep all requests offline.
	isolateConfig(t)
	t.Setenv("GECKO_SHELL", "/not/the/shell")
	for _, tt := range []struct {
		name                  string
		flags                 []string
		command               string
		code                  int
		wantRun, wantGuidance bool
	}{
		{"default declines pipe", nil, "printf EXECUTED", 0, false, true},
		{"dry run", []string{"--dry-run"}, "printf EXECUTED", 0, false, false},
		{"execute short", []string{"-x"}, "printf EXECUTED", 0, true, false},
		{"execute long", []string{"--execute"}, "printf EXECUTED", 0, true, false},
		{"child exit status", []string{"-x"}, "exit 23", 23, false, false},
		{"exclusive flags", []string{"-x", "--dry-run"}, "printf EXECUTED", 1, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			registry := provider.NewRegistry()
			registry.Register(func(provider.Config) (provider.Provider, error) {
				return mock.New(provider.Suggestion{Command: tt.command, Explanation: "fixed test suggestion", Risk: provider.RiskHigh}, nil), nil
			}, "openai")
			cmd := newRootCmdWithRegistry(registry)
			var out, stderr bytes.Buffer
			input := strings.NewReader("y\n")
			cmd.SetIn(input)
			cmd.SetOut(&out)
			cmd.SetErr(&stderr)
			args := append([]string{"--shell", "/bin/sh"}, tt.flags...)
			cmd.SetArgs(append(args, "test request"))
			err := cmd.ExecuteContext(context.Background())
			if got := runner.ExitCode(context.Background(), err); got != tt.code {
				t.Fatalf("exit code %d, want %d: %v", got, tt.code, err)
			}
			if got := strings.HasSuffix(out.String(), "EXECUTED"); got != tt.wantRun {
				t.Fatalf("executed=%v want=%v output=%q", got, tt.wantRun, &out)
			}
			if got := strings.Contains(stderr.String(), "--execute to run or --dry-run"); got != tt.wantGuidance {
				t.Fatalf("guidance=%v: %q", got, &stderr)
			}
			if strings.Contains(stderr.String(), "Run?") {
				t.Fatal("unexpected confirmation")
			}
			if (len(tt.flags) == 0 || tt.flags[0] == "--dry-run") && input.Len() != len("y\n") {
				t.Fatal("read input without execution")
			}
			if tt.name != "exclusive flags" && !strings.Contains(out.String(), "risk:        high") {
				t.Fatalf("suggestion not displayed: %q", &out)
			}
		})
	}
}

func TestRunnerIntegrationExecuteInheritsInput(t *testing.T) {
	isolateConfig(t)
	registry := provider.NewRegistry()
	registry.Register(func(provider.Config) (provider.Provider, error) {
		return mock.New(provider.Suggestion{Command: `read value; printf '%s' "$value"`}, nil), nil
	}, "openai")
	cmd := newRootCmdWithRegistry(registry)
	var out bytes.Buffer
	cmd.SetIn(strings.NewReader("payload\n"))
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"-x", "--shell", "/bin/sh", "test request"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out.String(), "payload") {
		t.Fatalf("input not inherited: %q", &out)
	}
}

// isolateConfig prevents tests from consulting a user's config or credentials.
func isolateConfig(t *testing.T) {
	t.Helper()
	t.Setenv("GECKO_CONFIG", filepath.Join(t.TempDir(), "absent.toml"))
	for _, key := range []string{"GECKO_PROVIDER", "GECKO_MODEL", "GECKO_BASE_URL", "GECKO_SHELL", "OPENAI_API_KEY"} {
		t.Setenv(key, "")
	}
}

func TestRootValidationBeforeProvider(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"missing query", nil, "requires at least 1 arg"},
		{"blank query", []string{"  "}, "empty query"},
		{"unknown flag", []string{"--unknown", "query"}, "unknown flag"},
		{"missing flag value", []string{"--model"}, "flag needs an argument"},
		{"invalid boolean", []string{"--execute=maybe", "query"}, "invalid syntax"},
		{"exclusive flags", []string{"--execute", "--dry-run", "query"}, "none of the others can be"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			isolateConfig(t)
			registry := provider.NewRegistry()
			registry.Register(func(provider.Config) (provider.Provider, error) {
				t.Fatal("provider constructed for invalid invocation")
				return nil, nil
			}, "openai")
			cmd := newRootCmdWithRegistry(registry)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(tt.args)
			err := cmd.ExecuteContext(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v; want error containing %q", err, tt.want)
			}
		})
	}
}

func TestRootConfigPrecedence(t *testing.T) {
	for _, tt := range []struct {
		name  string
		env   bool
		flags []string
		want  provider.Config
		shell string
	}{
		{"file", false, nil, provider.Config{Name: "file", Model: "file-model", BaseURL: "https://file.invalid", APIKey: "file-key"}, "file-shell"},
		{"environment", true, nil, provider.Config{Name: "env", Model: "env-model", BaseURL: "https://env.invalid", APIKey: "env-key"}, "env-shell"},
		{"flags", true, []string{"-p", "flag", "-m", "flag-model", "--base-url", "https://flag.invalid", "--shell", "/test/flag-shell"}, provider.Config{Name: "flag", Model: "flag-model", BaseURL: "https://flag.invalid", APIKey: "flag-key"}, "flag-shell"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			isolateConfig(t)
			path := filepath.Join(t.TempDir(), "config.toml")
			body := `default_provider = "file"
shell = "/test/file-shell"
[providers.file]
model = "file-model"
base_url = "https://file.invalid"
api_key = "file-key"
[providers.env]
model = "profile-model"
api_key = "env-key"
[providers.flag]
model = "profile-model"
api_key = "flag-key"
`
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GECKO_CONFIG", path)
			if tt.env {
				t.Setenv("GECKO_PROVIDER", "env")
				t.Setenv("GECKO_MODEL", "env-model")
				t.Setenv("GECKO_BASE_URL", "https://env.invalid")
				t.Setenv("GECKO_SHELL", "/test/env-shell")
			}
			p := mock.New(provider.Suggestion{Command: "printf offline"}, nil)
			registry := provider.NewRegistry()
			var configs []provider.Config
			registry.Register(func(cfg provider.Config) (provider.Provider, error) {
				configs = append(configs, cfg)
				return p, nil
			}, "openai")
			cmd := newRootCmdWithRegistry(registry)
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(append(append([]string{"--dry-run"}, tt.flags...), "--", "-literal query"))
			if err := cmd.ExecuteContext(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(configs) != 1 || configs[0] != tt.want {
				t.Fatalf("configs = %+v, want %+v", configs, tt.want)
			}
			requests := p.Requests()
			if len(requests) != 1 || requests[0].User != "-literal query" || !strings.Contains(requests[0].System, "- Shell: "+tt.shell+"\n") {
				t.Fatalf("requests = %+v", requests)
			}
			if !strings.Contains(out.String(), "printf offline") {
				t.Fatalf("suggestion not displayed: %q", &out)
			}
		})
	}
}

func TestRootProviderFailures(t *testing.T) {
	failure := errors.New("offline provider failure")
	refusal := &provider.NoCommandError{Reason: "not a terminal request"}
	for _, tt := range []struct {
		name                   string
		err                    error
		factoryError, canceled bool
		code                   int
	}{
		{"suggest error", failure, false, false, 1},
		{"model refusal", refusal, false, false, 1},
		{"factory error", failure, true, false, 1},
		{"canceled context", nil, false, true, 130},
	} {
		t.Run(tt.name, func(t *testing.T) {
			isolateConfig(t)
			p := mock.New(provider.Suggestion{Command: "printf NEVER_RUN"}, tt.err)
			registry := provider.NewRegistry()
			registry.Register(func(provider.Config) (provider.Provider, error) {
				if tt.factoryError {
					return nil, tt.err
				}
				return p, nil
			}, "openai")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wantErr := tt.err
			if tt.canceled {
				cancel()
				wantErr = context.Canceled
			}
			cmd := newRootCmdWithRegistry(registry)
			var out, stderr bytes.Buffer
			input := strings.NewReader("untouched\n")
			cmd.SetIn(input)
			cmd.SetOut(&out)
			cmd.SetErr(&stderr)
			cmd.SetArgs([]string{"--execute", "--shell", "/bin/sh", "test request"})
			err := cmd.ExecuteContext(ctx)
			if !errors.Is(err, wantErr) {
				t.Fatalf("got %v, want %v", err, wantErr)
			}
			if got := runner.ExitCode(ctx, err); got != tt.code {
				t.Fatalf("exit code %d, want %d", got, tt.code)
			}
			if out.Len() != 0 || stderr.Len() != 0 || input.Len() != len("untouched\n") {
				t.Fatal("failure displayed a suggestion, ran a command, or consumed input")
			}
			wantCalls := 1
			if tt.factoryError {
				wantCalls = 0
			}
			if len(p.Requests()) != wantCalls {
				t.Fatalf("requests = %+v", p.Requests())
			}
		})
	}
}

func TestMockIsNotPublicBackend(t *testing.T) {
	if got := strings.Join(newRegistry().Types(), ","); got != "openai,openai-compatible" {
		t.Fatalf("public backends = %s", got)
	}
}
