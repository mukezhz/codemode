// codemode-worker runs one sandbox program for sandbox/subprocess and exits.
// It reads nothing from its environment: every setting arrives in the run message.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"runtime/metrics"
	"time"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/internal/wire"
	"github.com/mukezhz/codemode/sandbox"

	_ "github.com/mukezhz/codemode/sandbox/goja"
	_ "github.com/mukezhz/codemode/sandbox/lua"
	_ "github.com/mukezhz/codemode/sandbox/starlark"
)

// ExitHeap tells the host the heap watchdog fired.
const ExitHeap = 3

func main() {
	writer := wire.NewWriter(os.Stdout)
	if err := run(wire.NewReader(os.Stdin, 64<<20), writer); err != nil {
		_ = writer.Write(wire.Message{Type: wire.TypeError, Error: err.Error()})
		os.Exit(1)
	}
}

func run(reader *wire.Reader, writer *wire.Writer) error {
	start, err := reader.Read()
	if err != nil {
		return err
	}
	if start.Type != wire.TypeRun || start.Limits == nil {
		return fmt.Errorf("expected a run message, got %q", start.Type)
	}
	if start.V != wire.Version {
		return fmt.Errorf("protocol version %d, worker speaks %d", start.V, wire.Version)
	}
	limits := *start.Limits
	if err := applyLimits(limits); err != nil {
		return err
	}
	watchHeap(uint64(limits.MaxHeapBytes))

	runner, err := sandbox.New(sandbox.Options{
		Engine:           start.Engine,
		Timeout:          time.Duration(limits.TimeoutMS) * time.Millisecond,
		MaxCalls:         limits.MaxCalls,
		MaxResponseBytes: limits.MaxResponseBytes,
	})
	if err != nil {
		return err
	}
	value, err := runner.Run(context.Background(), &bridge{reader: reader, writer: writer}, start.Source)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("program returned a value that is not JSON: %w", err)
	}
	return writer.Write(wire.Message{Type: wire.TypeDone, Value: encoded})
}

// watchHeap exits rather than letting the kernel or host pick a victim; RLIMIT_AS cannot cap a Go process, so this is the cap on every OS.
func watchHeap(limit uint64) {
	debug.SetMemoryLimit(int64(limit))
	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	go func() {
		for range time.Tick(5 * time.Millisecond) {
			metrics.Read(sample)
			if sample[0].Value.Uint64() > limit {
				os.Exit(ExitHeap)
			}
		}
	}()
}

// bridge is the worker's CodeMode: every Execute is a round trip to the host, which owns the registry.
type bridge struct {
	reader *wire.Reader
	writer *wire.Writer
	id     int
}

func (bridge *bridge) Search(context.Context, string, int) ([]capability.Match, error) {
	return nil, errors.New("search is not exposed to the sandbox")
}

func (bridge *bridge) Execute(_ context.Context, name string, input json.RawMessage) (json.RawMessage, error) {
	bridge.id++
	if err := bridge.writer.Write(wire.Message{Type: wire.TypeCall, ID: bridge.id, Name: name, Input: input}); err != nil {
		return nil, err
	}
	reply, err := bridge.reader.Read()
	if err != nil {
		return nil, err
	}
	if reply.Type != wire.TypeResult || reply.ID != bridge.id {
		return nil, fmt.Errorf("expected result %d, got %s %d", bridge.id, reply.Type, reply.ID)
	}
	if reply.Error != "" {
		return nil, errors.New(reply.Error)
	}
	return reply.Output, nil
}
