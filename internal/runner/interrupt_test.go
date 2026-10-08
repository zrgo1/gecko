package runner

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"testing"
	"time"

	"github.com/zrgo/gecko/internal/provider"
)

type readyReader struct{ io.Reader }

func (r readyReader) Read(p []byte) (int, error) {
	fmt.Fprintln(os.Stdout, "ready")
	return r.Reader.Read(p)
}

// Isolate real SIGINT delivery from the test suite. This exercises the same
// NotifyContext boundary as the CLI, both while reading and while in the shell.
func TestInterrupt(t *testing.T) {
	if mode := os.Getenv("GECKO_RUNNER_INTERRUPT_TEST"); mode != "" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		r := New(os.Stdin, os.Stdout, io.Discard)
		r.Interactive = func() bool { return true }
		s := provider.Suggestion{Command: "unused"}
		if mode == "confirm" {
			r.In = readyReader{os.Stdin}
		} else {
			s.Command = "printf 'ready\\n'; read value"
		}
		err := r.Run(ctx, s, "/bin/sh", false, mode == "execute")
		os.Exit(ExitCode(ctx, err))
	}
	for _, mode := range []string{"confirm", "execute"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestInterrupt$")
			cmd.Env = append(os.Environ(), "GECKO_RUNNER_INTERRUPT_TEST="+mode)
			in, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer in.Close()
			out, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			cmd.Stderr = os.Stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			scanner := bufio.NewScanner(out)
			ready := false
			for scanner.Scan() {
				if scanner.Text() == "ready" {
					ready = true
					break
				}
			}
			if !ready {
				_ = cmd.Wait()
				t.Fatalf("helper never became ready: %v", scanner.Err())
			}
			if err := cmd.Process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
			err = cmd.Wait()
			if ctx.Err() != nil {
				t.Fatal("SIGINT did not terminate promptly")
			}
			if ExitCode(context.Background(), err) != 130 {
				t.Fatalf("SIGINT exit: %v", err)
			}
		})
	}
}
