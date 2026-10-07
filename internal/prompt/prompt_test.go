package prompt

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func fakeLookPath(installed ...string) LookPath {
	set := map[string]bool{}
	for _, s := range installed {
		set[s] = true
	}
	return func(name string) (string, error) {
		if set[name] {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
}

func TestDetect(t *testing.T) {
	env := Detect("/opt/homebrew/bin/fish", fakeLookPath("jq", "rg", "nothing-we-probe"))

	if env.Shell != "fish" {
		t.Errorf("Shell = %q, want base name", env.Shell)
	}
	// Order follows Probed, not lookup order; unprobed names are ignored.
	if want := []string{"rg", "jq"}; !reflect.DeepEqual(env.Tools, want) {
		t.Errorf("Tools = %v, want %v", env.Tools, want)
	}
	if wd, _ := os.Getwd(); env.Cwd != wd {
		t.Errorf("Cwd = %q, want %q", env.Cwd, wd)
	}
}

func TestShellPath(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if got := ShellPath("/bin/bash", env(map[string]string{"SHELL": "/bin/zsh"})); got != "/bin/bash" {
		t.Errorf("configured should win, got %s", got)
	}
	if got := ShellPath("", env(map[string]string{"SHELL": "/bin/zsh"})); got != "/bin/zsh" {
		t.Errorf("$SHELL fallback, got %s", got)
	}
	if got := ShellPath("", env(nil)); got != "/bin/sh" {
		t.Errorf("final fallback, got %s", got)
	}
}

func TestBuildSystemPrompt(t *testing.T) {
	req := Build(Env{OS: "darwin", Shell: "zsh", Cwd: "/Users/me/proj", Tools: []string{"rg", "fd", "git"}}, "", "q")

	for _, want := range []string{
		"- OS: macOS (BSD userland",
		"- Shell: zsh",
		"- Working directory: /Users/me/proj",
		"- Extra tools installed: rg (search file contents), fd (find files by name), git\n",
		`{"command": "<the shell command>"`,
		`set "command" to ""`,
	} {
		if !strings.Contains(req.System, want) {
			t.Errorf("system prompt missing %q", want)
		}
	}
}

func TestBuildNoTools(t *testing.T) {
	req := Build(Env{OS: "linux", Shell: "bash", Cwd: "/"}, "", "q")
	if !strings.Contains(req.System, "none detected") || !strings.Contains(req.System, "Linux (GNU userland)") {
		t.Errorf("unexpected system prompt:\n%s", req.System)
	}
}

func TestBuildUserPrompt(t *testing.T) {
	env := Env{OS: "linux", Shell: "bash", Cwd: "/"}

	if got := Build(env, "", "list big files").User; got != "list big files" {
		t.Errorf("no hint: got %q", got)
	}
	got := Build(env, "grep", "files starting with t").User
	if !strings.HasPrefix(got, "Use `grep` as the main tool") || !strings.HasSuffix(got, "\n\nfiles starting with t") {
		t.Errorf("with hint: got %q", got)
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	env := Env{OS: "darwin", Shell: "zsh", Cwd: "/x", Tools: []string{"rg"}}
	if Build(env, "rg", "q") != Build(env, "rg", "q") {
		t.Fatal("same input should give the same request")
	}
}
