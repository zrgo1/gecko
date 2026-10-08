package runner

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zrgo/gecko/internal/provider"
)

func TestConfirmation(t *testing.T) {
	for _, tt := range []struct {
		name, input, wantCommand     string
		dryRun, execute, interactive bool
		prompts                      int
	}{
		{"yes", "y\n", "original", false, false, true, 1},
		{"yes word", " YES \n", "original", false, false, true, 1},
		{"no", "n\n", "", false, false, true, 1},
		{"default", "\n", "", false, false, true, 1},
		{"EOF", "", "", false, false, true, 1},
		{"unterminated yes", "y", "", false, false, true, 1},
		{"invalid", "maybe\n", "", false, false, true, 1},
		{"edit", "e\nprintf edited\ny\n", "printf edited", false, false, true, 2},
		{"edit decline", "e\nprintf edited\nn\n", "", false, false, true, 2},
		{"edit EOF", "e\n", "", false, false, true, 1},
		{"edit empty", "e\n\n", "", false, false, true, 1},
		{"dry run", "y\n", "", true, false, true, 0},
		{"execute", "", "original", false, true, false, 0},
		{"piped yes", "y\n", "", false, false, false, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			in := strings.NewReader(tt.input)
			var out, stderr bytes.Buffer
			r := New(in, &out, &stderr)
			r.Interactive = func() bool {
				if tt.dryRun || tt.execute {
					t.Fatal("checked terminal in bypass mode")
				}
				return tt.interactive
			}
			var gotCommand string
			calls := 0
			r.Execute = func(_ context.Context, shell, command string, stdin io.Reader, stdout, errout io.Writer) error {
				calls++
				gotCommand = command
				if shell != "/chosen/shell" || stdin != in || stdout != &out || errout != &stderr {
					t.Fatal("I/O or shell not inherited")
				}
				return nil
			}
			err := r.Run(context.Background(), provider.Suggestion{Command: "original", Explanation: "why", Risk: provider.RiskHigh}, "/chosen/shell", tt.dryRun, tt.execute)
			if err != nil {
				t.Fatal(err)
			}
			if gotCommand != tt.wantCommand || (calls > 0) != (tt.wantCommand != "") {
				t.Fatalf("executed %q (%d calls), want %q", gotCommand, calls, tt.wantCommand)
			}
			if strings.Count(stderr.String(), "Run? [y/N/e]") != tt.prompts {
				t.Fatalf("prompts: %q", stderr.String())
			}
			for _, text := range []string{"original", "why", "risk:        high"} {
				if !strings.Contains(out.String(), text) {
					t.Fatalf("missing %q: %s", text, &out)
				}
			}
			if tt.dryRun || tt.execute || !tt.interactive {
				if in.Len() != len(tt.input) {
					t.Fatal("read input without confirmation")
				}
			}
			if !tt.interactive && !tt.execute {
				if !strings.Contains(stderr.String(), "--execute") || !strings.Contains(stderr.String(), "--dry-run") {
					t.Fatal("missing piped-input guidance")
				}
			}
		})
	}
}

func TestConfirmationPreservesCommandInput(t *testing.T) {
	in := strings.NewReader("y\ncommand input\n")
	var out bytes.Buffer
	r := New(in, &out, io.Discard)
	r.Interactive = func() bool { return true }
	// Only a fixed, harmless shell command is executed in this test.
	err := r.Run(context.Background(), provider.Suggestion{Command: `read value; printf '%s' "$value"`}, "/bin/sh", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out.String(), "command input") {
		t.Fatalf("lost command input: %q", out.String())
	}
}

type notifyingReader struct {
	io.Reader
	started chan struct{}
}

func (r notifyingReader) Read(p []byte) (int, error) {
	select {
	case r.started <- struct{}{}:
	default:
	}
	return r.Reader.Read(p)
}

type notifyingWriter struct{ started chan struct{} }

func (w notifyingWriter) Write(p []byte) (int, error) {
	select {
	case w.started <- struct{}{}:
	default:
	}
	return len(p), nil
}

func TestConfirmationCancellation(t *testing.T) {
	in, writer := io.Pipe()
	defer in.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 1)
	r := New(notifyingReader{in, started}, io.Discard, io.Discard)
	r.Interactive = func() bool { return true }
	r.Execute = func(context.Context, string, string, io.Reader, io.Writer, io.Writer) error {
		t.Error("executed after cancellation")
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx, provider.Suggestion{Command: "unused"}, "/bin/sh", false, false) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("confirmation did not read")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("confirmation blocked cancellation")
	}
}

func TestExecuteShell(t *testing.T) {
	var out, stderr bytes.Buffer
	err := ExecuteShell(context.Background(), "/bin/sh", `printf out; printf err >&2; exit 7`, nil, &out, &stderr)
	if ExitCode(context.Background(), err) != 7 || out.String() != "out" || stderr.String() != "err" {
		t.Fatalf("err=%v out=%q stderr=%q", err, &out, &stderr)
	}
	out.Reset()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	err = ExecuteShell(context.Background(), "/bin/sh", `printf '%s' "$PWD"`, nil, &out, io.Discard)
	if err != nil || out.String() != cwd {
		t.Fatalf("cwd=%q want %q, err=%v", &out, cwd, err)
	}
	if ExitCode(context.Background(), nil) != 0 || ExitCode(context.Background(), errors.New("start failed")) != 1 {
		t.Fatal("wrong success/error exit code")
	}
}

func TestExecuteShellCancellation(t *testing.T) {
	// A shell builtin waits for input; closing the file also guarantees cleanup.
	in, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	started := make(chan struct{}, 1)
	go func() {
		done <- ExecuteShell(ctx, "/bin/sh", "printf ready; read value", in, notifyingWriter{started}, io.Discard)
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("shell did not start")
	}
	cancel()
	select {
	case err := <-done:
		if ExitCode(ctx, err) != 130 {
			t.Fatalf("exit code: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("execution blocked cancellation")
	}
}

func TestTerminalDetectionRejectsPipesAndDevNull(t *testing.T) {
	in, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	defer writer.Close()
	if New(in, io.Discard, io.Discard).Interactive() {
		t.Fatal("pipe is not a terminal")
	}
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	if New(null, io.Discard, io.Discard).Interactive() {
		t.Fatal("/dev/null is not a terminal")
	}
}
