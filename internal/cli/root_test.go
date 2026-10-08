package cli

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zrgo/gecko/internal/provider"
	"github.com/zrgo/gecko/internal/runner"
)

type fixedProvider struct{ suggestion provider.Suggestion }

func (p fixedProvider) Suggest(context.Context, provider.Request) (provider.Suggestion, error) {
	return p.suggestion, nil
}

func TestRunnerIntegration(t *testing.T) {
	// An absent temp config and in-process provider keep all requests offline.
	t.Setenv("GECKO_CONFIG", filepath.Join(t.TempDir(), "absent.toml"))
	t.Setenv("GECKO_PROVIDER", "")
	t.Setenv("GECKO_MODEL", "")
	t.Setenv("GECKO_BASE_URL", "")
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
				return fixedProvider{provider.Suggestion{Command: tt.command, Explanation: "fixed test suggestion", Risk: provider.RiskHigh}}, nil
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
	t.Setenv("GECKO_CONFIG", filepath.Join(t.TempDir(), "absent.toml"))
	t.Setenv("GECKO_PROVIDER", "")
	registry := provider.NewRegistry()
	registry.Register(func(provider.Config) (provider.Provider, error) {
		return fixedProvider{provider.Suggestion{Command: `read value; printf '%s' "$value"`}}, nil
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
