// Package subprocess runs each program in a codemode-worker child process.
// Every limit that matters is enforced here, outside the child.
package subprocess

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
	"github.com/mukezhz/codemode/internal/wire"
	"github.com/mukezhz/codemode/sandbox"
)

// exitHeap mirrors cmd/codemode-worker's ExitHeap.
const exitHeap = 3

func init() {
	sandbox.RegisterIsolation(sandbox.Isolation{Name: sandbox.IsolationProcess, New: New})
}

type Runner struct {
	options sandbox.Options
	inner   sandbox.Runner
	engine  string
}

var _ sandbox.Runner = (*Runner)(nil)

// New runs options.Engine in codemode-worker. That engine must also be imported
// here, for its prompt. New starts one worker and runs a trivial program, so a
// missing binary, a version mismatch or a broken container command fails now,
// not on the first request.
func New(options sandbox.Options) (sandbox.Runner, error) {
	options, err := options.Checked()
	if err != nil {
		return nil, err
	}
	engine := strings.ToLower(strings.TrimSpace(options.Engine))
	if engine == "" {
		engine = sandbox.DefaultEngine
	}
	inner, err := sandbox.New(sandbox.Options{Engine: engine})
	if err != nil {
		return nil, err
	}
	if len(options.Worker) == 0 {
		if options.WorkerContainer {
			return nil, errors.New("worker container is set but no worker command is: set SANDBOX_WORKER to the docker run argv")
		}
		path, err := exec.LookPath("codemode-worker")
		if err != nil {
			return nil, fmt.Errorf("process isolation needs codemode-worker: install it on PATH or set SANDBOX_WORKER: %w", err)
		}
		options.Worker = []string{path}
	}
	runner := &Runner{options: options, inner: inner, engine: engine}
	if err := runner.check(); err != nil {
		inner.Close()
		return nil, err
	}
	return runner, nil
}

// checkTimeout covers a container start, which can take longer than a program's own timeout.
const checkTimeout = 30 * time.Second

// check is "return 1" because that is valid in every engine's language.
func (runner *Runner) check() error {
	probe := *runner
	probe.options.Timeout = max(runner.options.Timeout, checkTimeout)
	value, err := probe.Run(context.Background(), refuseAll{}, "return 1")
	if err != nil {
		return fmt.Errorf("codemode-worker check failed (%s): %w", strings.Join(runner.options.Worker, " "), err)
	}
	if value != float64(1) {
		return fmt.Errorf("codemode-worker check failed (%s): returned %v, want 1", strings.Join(runner.options.Worker, " "), value)
	}
	return nil
}

type refuseAll struct{}

func (refuseAll) Search(context.Context, string, int) ([]capability.Match, error) {
	return nil, errors.New("no capabilities during the worker check")
}

func (refuseAll) Execute(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return nil, errors.New("no capabilities during the worker check")
}

func (runner *Runner) Language() string { return runner.inner.Language() }

func (runner *Runner) Prompt(matches []capability.Match) string { return runner.inner.Prompt(matches) }

func (runner *Runner) Name() string {
	where := "child process"
	if runner.options.WorkerContainer {
		where = "container"
	}
	return fmt.Sprintf("%s / %s (%s, %s timeout, %d call budget, %d MiB heap)",
		runner.engine, runner.inner.Language(), where, runner.options.Timeout, runner.options.MaxCalls,
		runner.options.MaxHeapBytes>>20)
}

func (runner *Runner) Close() error { return runner.inner.Close() }

func (runner *Runner) Run(ctx context.Context, mode codemode.CodeMode, source string) (any, error) {
	if mode == nil {
		return nil, errors.New("code mode runtime is required")
	}
	ctx, cancel := context.WithTimeout(ctx, runner.options.Timeout)
	defer cancel()

	dir, err := os.MkdirTemp("", "codemode-worker-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	stderr := &tail{limit: 64 << 10}
	namespaces := unshare.Load() && !runner.options.WorkerContainer
	cmd, stdin, stdout, err := runner.spawn(ctx, dir, stderr, namespaces)
	if err != nil && namespaces && denied(err) {
		unshare.Store(false)
		cmd, stdin, stdout, err = runner.spawn(ctx, dir, stderr, false)
	}
	if err != nil {
		return nil, fmt.Errorf("start codemode-worker: %w", err)
	}

	value, err := runner.converse(ctx, mode, source, wire.NewReader(stdout, runner.options.MaxResponseBytes+4096), wire.NewWriter(stdin))
	stopped := ctx.Err()
	cancel()
	waitErr := cmd.Wait()
	if err == nil {
		return value, nil
	}
	if stopped != nil {
		return nil, fmt.Errorf("sandbox stopped: %w", stopped)
	}
	var exit *exec.ExitError
	if errors.As(waitErr, &exit) && exit.ExitCode() == exitHeap {
		return nil, fmt.Errorf("sandbox: memory limit of %d bytes exceeded", runner.options.MaxHeapBytes)
	}
	if detail := strings.TrimSpace(stderr.String()); detail != "" && !isProgramError(err) {
		return nil, fmt.Errorf("%w (worker: %v: %s)", err, waitErr, detail)
	}
	return nil, err
}

// unshare flips off for the process lifetime once the kernel refuses unprivileged namespaces.
// ponytail: silent fallback; surface it in Name() if operators need to see it.
var unshare = func() *atomic.Bool { flag := new(atomic.Bool); flag.Store(canUnshare); return flag }()

func (runner *Runner) spawn(ctx context.Context, dir string, stderr io.Writer, namespaces bool) (*exec.Cmd, io.WriteCloser, io.ReadCloser, error) {
	argv := runner.options.Worker
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = []string{}
	cmd.Dir = dir
	cmd.WaitDelay = time.Second
	cmd.Stderr = stderr
	isolate(cmd, namespaces)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	return cmd, stdin, stdout, cmd.Start()
}

type programError struct{ message string }

func (err programError) Error() string { return err.message }

func isProgramError(err error) bool {
	var program programError
	return errors.As(err, &program)
}

// converse is the only code that reads the child; it trusts no field it did not check.
func (runner *Runner) converse(ctx context.Context, mode codemode.CodeMode, source string,
	reader *wire.Reader, writer *wire.Writer) (any, error) {
	err := writer.Write(wire.Message{V: wire.Version, Type: wire.TypeRun, Engine: runner.engine, Source: source,
		Limits: &wire.Limits{
			TimeoutMS:        runner.options.Timeout.Milliseconds(),
			MaxCalls:         runner.options.MaxCalls,
			MaxResponseBytes: runner.options.MaxResponseBytes,
			MaxHeapBytes:     runner.options.MaxHeapBytes,
		}})
	if err != nil {
		return nil, fmt.Errorf("worker protocol error: %w", err)
	}

	calls := 0
	for {
		message, err := reader.Read()
		if err != nil {
			return nil, fmt.Errorf("worker protocol error: %w", err)
		}
		switch message.Type {
		case wire.TypeCall:
			calls++
			if message.ID != calls {
				return nil, fmt.Errorf("worker protocol error: call id %d, want %d", message.ID, calls)
			}
			if calls > runner.options.MaxCalls {
				return nil, programError{fmt.Sprintf("sandbox: call budget of %d exhausted", runner.options.MaxCalls)}
			}
			reply := wire.Message{Type: wire.TypeResult, ID: message.ID}
			output, err := mode.Execute(ctx, message.Name, message.Input)
			if err == nil {
				err = runner.options.CheckResponse(message.Name, output)
			}
			if err != nil {
				reply.Error = err.Error()
			} else {
				reply.Output = output
			}
			if err := writer.Write(reply); err != nil {
				return nil, fmt.Errorf("worker protocol error: %w", err)
			}
		case wire.TypeDone:
			return decode(message.Value)
		case wire.TypeError:
			return nil, programError{message.Error}
		default:
			return nil, fmt.Errorf("worker protocol error: unexpected %q message", message.Type)
		}
	}
}

type tail struct {
	bytes.Buffer
	limit int
}

func (tail *tail) Write(p []byte) (int, error) {
	if room := tail.limit - tail.Len(); room > 0 {
		tail.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func decode(value []byte) (any, error) {
	if len(value) == 0 {
		return nil, nil
	}
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return nil, fmt.Errorf("worker protocol error: %w", err)
	}
	return decoded, nil
}
