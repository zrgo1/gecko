// Package runner displays, confirms, and executes a suggested shell command.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/term"

	"github.com/zrgo/gecko/internal/provider"
)

// Executor is the shell execution boundary. The input must be the same reader
// used for confirmation, with no read-ahead consuming the command's input.
type Executor func(context.Context, string, string, io.Reader, io.Writer, io.Writer) error

// Runner exposes I/O and execution boundaries for deterministic tests.
type Runner struct {
	In          io.Reader
	Out         io.Writer
	Err         io.Writer
	Interactive func() bool
	Execute     Executor
}

// New uses real terminal detection and shell execution with the supplied I/O.
func New(in io.Reader, out, stderr io.Writer) *Runner {
	return &Runner{
		In: in, Out: out, Err: stderr,
		Interactive: func() bool {
			f, ok := in.(*os.File)
			return ok && term.IsTerminal(int(f.Fd()))
		},
		Execute: ExecuteShell,
	}
}

// Run always displays the suggestion. Dry run never reads input or executes;
// execute bypasses all confirmation, including model risk and terminal checks.
func (r *Runner) Run(ctx context.Context, s provider.Suggestion, shell string, dryRun, execute bool) error {
	fmt.Fprintf(r.Out, "command:     %s\nexplanation: %s\nrisk:        %s\n", s.Command, s.Explanation, s.Risk)
	if dryRun {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !execute {
		if !r.Interactive() {
			fmt.Fprintln(r.Err, "gecko: not executed: confirmation requires a terminal; use --execute to run or --dry-run to print only")
			return nil
		}
		for {
			fmt.Fprint(r.Err, "Run? [y/N/e] ")
			answer, err := readLine(ctx, r.In)
			if errors.Is(err, io.EOF) {
				return r.decline()
			}
			if err != nil {
				return err
			}
			switch strings.ToLower(strings.TrimSpace(answer)) {
			case "y", "yes":
				execute = true
			case "e":
				fmt.Fprint(r.Err, "Replacement command: ")
				replacement, err := readLine(ctx, r.In)
				if errors.Is(err, io.EOF) {
					return r.decline()
				}
				if err != nil {
					return err
				}
				if strings.TrimSpace(replacement) == "" {
					return r.decline()
				}
				s.Command = replacement
				fmt.Fprintf(r.Out, "command (edited): %s\n", s.Command)
				continue
			default:
				return r.decline()
			}
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.Execute(ctx, shell, s.Command, r.In, r.Out, r.Err)
}

func (r *Runner) decline() error {
	fmt.Fprintln(r.Err, "gecko: not executed")
	return nil
}

// readLine reads exactly through the newline, never buffering shell input.
// A canceled read may remain blocked until input closes, but no further input
// is read or execution attempted after cancellation (the CLI then exits).
func readLine(ctx context.Context, in io.Reader) (string, error) {
	type result struct {
		line string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		var line strings.Builder
		var b [1]byte
		for {
			n, err := in.Read(b[:])
			if n > 0 {
				if b[0] == '\n' {
					done <- result{line.String(), nil}
					return
				}
				line.WriteByte(b[0])
			}
			if err != nil {
				// Even an unterminated "yes" is a safe decline on EOF.
				done <- result{line.String(), err}
				return
			}
			if err := ctx.Err(); err != nil {
				done <- result{"", err}
				return
			}
		}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case got := <-done:
		return got.line, got.err
	}
}

// ExecuteShell inherits the current working directory and the supplied streams.
// CommandContext ensures cancellation still works with the CLI's SIGINT handler.
func ExecuteShell(ctx context.Context, shell, command string, in io.Reader, out, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, shell, "-c", command)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, stderr
	return cmd.Run()
}

// ExitCode preserves shell exit status; canceled invocations take precedence.
func ExitCode(ctx context.Context, err error) int {
	if ctx.Err() != nil {
		return 130
	}
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if code := exit.ExitCode(); code >= 0 {
			return code
		}
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal())
		}
	}
	return 1
}
