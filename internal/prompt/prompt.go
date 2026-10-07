// Package prompt builds the messages sent to the model: a system prompt that
// describes the user's environment and the required reply format, and a user
// message carrying the request and optional tool hint.
package prompt

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/zrgo/gecko/internal/provider"
)

// Env describes where the command will run.
type Env struct {
	OS    string   // runtime.GOOS, e.g. "darwin", "linux"
	Shell string   // shell name, e.g. "zsh"
	Cwd   string   // working directory
	Tools []string // optional tools found on PATH, in probe order
}

// Probed lists optional tools worth telling the model about. Standard POSIX
// tools (grep, find, sed, awk, ...) are assumed and not listed. g-prefixed
// names are GNU variants commonly installed on macOS via Homebrew.
var Probed = []string{
	"rg", "fd", "fdfind", "ag", "jq", "yq", "fzf", "tree", "eza", "bat",
	"gsed", "gawk", "gfind", "gxargs", "gdate", "git", "curl", "wget", "docker",
}

// toolPurpose disambiguates tools models tend to confuse (e.g. using fd to
// search file contents). Tools without an entry are listed by name only.
var toolPurpose = map[string]string{
	"rg":     "search file contents",
	"ag":     "search file contents",
	"fd":     "find files by name",
	"fdfind": "fd: find files by name",
	"jq":     "JSON",
	"yq":     "YAML",
	"fzf":    "interactive, avoid",
	"eza":    "ls replacement",
	"bat":    "cat with highlighting",
	"gsed":   "GNU sed",
	"gawk":   "GNU awk",
	"gfind":  "GNU find",
	"gxargs": "GNU xargs",
	"gdate":  "GNU date",
}

// LookPath matches exec.LookPath so tests can stub tool detection.
type LookPath func(string) (string, error)

// Detect gathers the environment. shellPath is the shell that will run the
// command (see ShellPath); only its base name is reported to the model.
func Detect(shellPath string, lookPath LookPath) Env {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "(unknown)"
	}
	env := Env{OS: runtime.GOOS, Shell: filepath.Base(shellPath), Cwd: cwd}
	for _, t := range Probed {
		if _, err := lookPath(t); err == nil {
			env.Tools = append(env.Tools, t)
		}
	}
	return env
}

// DetectDefault is Detect with exec.LookPath.
func DetectDefault(shellPath string) Env { return Detect(shellPath, exec.LookPath) }

// ShellPath picks the shell used to run commands: the configured value,
// else $SHELL, else /bin/sh.
func ShellPath(configured string, getenv func(string) string) string {
	if configured != "" {
		return configured
	}
	if s := getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/sh"
}

// Build returns the request for query. hint is an optional tool name the
// user put before the query (e.g. "grep"); it is already verified to be on PATH.
func Build(env Env, hint, query string) provider.Request {
	return provider.Request{System: systemPrompt(env), User: userPrompt(hint, query)}
}

func systemPrompt(env Env) string {
	var b strings.Builder
	b.WriteString(`You are gecko, a command-line assistant. You translate a user's request into exactly one shell command that they can run in their terminal.

Environment:
`)
	fmt.Fprintf(&b, "- OS: %s\n", osLabel(env.OS))
	fmt.Fprintf(&b, "- Shell: %s\n", env.Shell)
	fmt.Fprintf(&b, "- Working directory: %s\n", env.Cwd)
	if len(env.Tools) > 0 {
		fmt.Fprintf(&b, "- Extra tools installed: %s\n", toolList(env.Tools))
	} else {
		b.WriteString("- Extra tools installed: none detected; use standard POSIX tools\n")
	}

	b.WriteString(`
Rules:
- Output one command. Pipelines, &&, and subshells are fine; do not output multiple alternatives.
- The command must work in the shell and OS above. Use flags supported by the tools that are actually installed.
- Unless the user says otherwise, operate on the working directory and do not recurse into subdirectories when the request is about "this directory".
- Prefer the simplest, most direct command. Prefer read-only commands when the request is ambiguous.
- Quote paths and search patterns so spaces and special characters are safe. A quoted glob is not expanded by the shell, so to filter by file name or type use the tool's own option (rg -g '*.go' or rg -t go, find -name '*.go', fd -e go).
- Only use flags that exist in the tool. When unsure, prefer common, well-known flags.
- Never use interactive programs (editors, pagers, prompts) and never add sudo unless the task cannot work without it.
- Do not invent file names or paths the user did not mention; use placeholders like <file> only if unavoidable.

Reply with only this JSON object and nothing else:
{"command": "<the shell command>", "explanation": "<one short sentence on what it does>", "risk": "low" | "medium" | "high"}

Risk levels:
- low: read-only (listing, searching, printing).
- medium: creates or modifies files or state in a recoverable way.
- high: deletes data, overwrites files, changes permissions or system settings, uses sudo, or is otherwise hard to undo.

If no single shell command can do what the user asks, or the request is not about the terminal, set "command" to "" and explain why in "explanation".`)
	return b.String()
}

func userPrompt(hint, query string) string {
	if hint == "" {
		return query
	}
	return fmt.Sprintf("Use `%s` as the main tool if it can do this.\n\n%s", hint, query)
}

func toolList(tools []string) string {
	parts := make([]string, len(tools))
	for i, t := range tools {
		if p, ok := toolPurpose[t]; ok {
			parts[i] = fmt.Sprintf("%s (%s)", t, p)
		} else {
			parts[i] = t
		}
	}
	return strings.Join(parts, ", ")
}

func osLabel(goos string) string {
	switch goos {
	case "darwin":
		return "macOS (BSD userland: use BSD-compatible flags for sed, find, stat, date, xargs, etc.)"
	case "linux":
		return "Linux (GNU userland)"
	case "windows":
		return "Windows"
	default:
		return goos
	}
}
