package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/internal/wire"
	"github.com/mukezhz/codemode/sandbox"

	_ "github.com/mukezhz/codemode/sandbox/goja"
)

const fakeFlag = "-fake-worker="

// TestMain doubles as a misbehaving worker: the host has no env to pass, so the behaviour rides in argv.
func TestMain(m *testing.M) {
	for _, arg := range os.Args[1:] {
		if behaviour, ok := strings.CutPrefix(arg, fakeFlag); ok {
			fakeWorker(behaviour)
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

func fakeWorker(behaviour string) {
	reader := wire.NewReader(os.Stdin, 1<<20)
	writer := wire.NewWriter(os.Stdout)
	start, err := reader.Read()
	if err != nil {
		os.Exit(2)
	}
	// Pass New's startup check, then misbehave on the real run.
	if start.Source == "return 1" {
		writer.Write(wire.Message{Type: wire.TypeDone, Value: []byte("1")})
		return
	}
	call := wire.Message{Type: wire.TypeCall, ID: 1, Name: "todos.list", Input: []byte(`{}`)}
	switch behaviour {
	case "garbage":
		fmt.Println("not json")
	case "wrong-id":
		call.ID = 7
		writer.Write(call)
	case "exit-mid-call":
		writer.Write(call)
		os.Exit(5)
	case "hang":
		time.Sleep(time.Hour)
	case "env":
		value, _ := json.Marshal(len(os.Environ()))
		writer.Write(wire.Message{Type: wire.TypeDone, Value: value})
	case "flood":
		for id := 1; ; id++ {
			call.ID = id
			if writer.Write(call) != nil {
				return
			}
			if _, err := reader.Read(); err != nil {
				return
			}
		}
	case "stderr":
		fmt.Fprintln(os.Stderr, "worker exploded")
		os.Exit(4)
	}
}

type fake struct{ calls int }

func (fake *fake) Search(context.Context, string, int) ([]capability.Match, error) { return nil, nil }

func (fake *fake) Execute(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	fake.calls++
	return json.RawMessage(`{"todos":[]}`), nil
}

func runFake(t *testing.T, behaviour string, options sandbox.Options) (any, *fake, error) {
	t.Helper()
	options.Worker = []string{os.Args[0], fakeFlag + behaviour}
	runner, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	mode := &fake{}
	value, err := runner.Run(context.Background(), mode, "ignored")
	return value, mode, err
}

func TestRunRejectsProtocolViolations(t *testing.T) {
	for behaviour, want := range map[string]string{
		"garbage":       "worker protocol error",
		"wrong-id":      "call id 7",
		"exit-mid-call": "worker protocol error",
		"stderr":        "worker exploded",
	} {
		t.Run(behaviour, func(t *testing.T) {
			_, _, err := runFake(t, behaviour, sandbox.Options{Timeout: 5 * time.Second})
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v, want %q", err, want)
			}
		})
	}
}

func TestRunKillsAHungWorker(t *testing.T) {
	started := time.Now()
	_, _, err := runFake(t, "hang", sandbox.Options{Timeout: 100 * time.Millisecond})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want a deadline", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("Run took %s after a 100ms timeout", elapsed)
	}
}

func TestRunCountsCallsOnTheHost(t *testing.T) {
	_, mode, err := runFake(t, "flood", sandbox.Options{Timeout: 5 * time.Second, MaxCalls: 4})
	if err == nil || !strings.Contains(err.Error(), "call budget of 4") {
		t.Fatalf("err = %v, want the call budget", err)
	}
	if mode.calls != 4 {
		t.Fatalf("calls = %d, want 4", mode.calls)
	}
}

func TestRunGivesTheWorkerNoEnvironment(t *testing.T) {
	t.Setenv("CODEMODE_TEST_SECRET", "leaked")
	value, _, err := runFake(t, "env", sandbox.Options{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if value != float64(0) {
		t.Fatalf("worker saw %v environment variables, want 0", value)
	}
}

func TestNewRequiresTheInnerEngine(t *testing.T) {
	_, err := New(sandbox.Options{Engine: "lua", Worker: []string{"unused"}})
	if err == nil || !strings.Contains(err.Error(), "sandbox/lua") {
		t.Fatalf("err = %v, want it to name the missing import", err)
	}
}
