package sandbox

import (
	"context"
	"strings"
	"testing"

	"github.com/mukezhz/code-mode/capability"
	"github.com/mukezhz/code-mode/codemode"
)

// stub is an engine registered only by this test, so the registration
// mechanics can be exercised without linking a real interpreter.
type stub struct{ options Options }

func (stub *stub) Run(context.Context, codemode.CodeMode, string) (any, error) { return nil, nil }
func (stub *stub) Language() string                                            { return "stub" }
func (stub *stub) Prompt([]capability.Match) string                            { return "" }
func (stub *stub) Close() error                                                { return nil }
func (stub *stub) Name() string {
	return "stub (" + stub.options.Timeout.String() + ")"
}

func init() {
	RegisterEngine(Engine{
		Name:    "stub",
		Aliases: []string{"stubby"},
		New:     func(options Options) (Runner, error) { return &stub{options: options}, nil },
	})
}

func TestNewSelectsARegisteredEngine(t *testing.T) {
	for _, name := range []string{"stub", "stubby"} {
		t.Setenv("SANDBOX_ENGINE", name)
		runner, err := New(Options{})
		if err != nil {
			t.Fatalf("New(%q) returned error: %v", name, err)
		}
		if runner.Language() != "stub" {
			t.Fatalf("Language() = %q, want %q", runner.Language(), "stub")
		}
	}
}

// A missing blank import is the common failure of this design, so the error
// has to name the package rather than say "unsupported".
func TestNewNamesTheMissingImportForAShippedEngine(t *testing.T) {
	t.Setenv("SANDBOX_ENGINE", EngineLua)
	_, err := New(Options{})
	if err == nil {
		t.Fatal("New returned no error for an unregistered engine")
	}
	if !strings.Contains(err.Error(), "sandbox/lua") {
		t.Fatalf("error = %v, want it to name the import path", err)
	}
}

func TestNewRejectsAnUnknownEngine(t *testing.T) {
	t.Setenv("SANDBOX_ENGINE", "wasm")
	_, err := New(Options{})
	if err == nil {
		t.Fatal("New returned no error for an unknown engine")
	}
	if strings.Contains(err.Error(), "sandbox/wasm") {
		t.Fatalf("error = %v, want it to not invent an import path", err)
	}
}

func TestNewReadsBoundsFromTheEnvironment(t *testing.T) {
	t.Setenv("SANDBOX_ENGINE", "stub")
	t.Setenv("SANDBOX_TIMEOUT", "7s")

	runner, err := New(Options{})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if !strings.Contains(runner.Name(), "7s") {
		t.Fatalf("Name() = %q, want the environment timeout", runner.Name())
	}

	// An explicitly set field wins over the environment.
	explicit, err := New(Options{Timeout: 3 * 1e9})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if !strings.Contains(explicit.Name(), "3s") {
		t.Fatalf("Name() = %q, want the explicit timeout to win", explicit.Name())
	}
}

func TestNewRejectsUnparseableBounds(t *testing.T) {
	t.Setenv("SANDBOX_ENGINE", "stub")
	t.Setenv("SANDBOX_TIMEOUT", "soon")
	if _, err := New(Options{}); err == nil {
		t.Fatal("New returned no error for an unparseable SANDBOX_TIMEOUT")
	}
}

func TestRegisteredEnginesListsCanonicalNamesOnly(t *testing.T) {
	for _, name := range RegisteredEngines() {
		if name == "stubby" {
			t.Fatal("RegisteredEngines listed an alias")
		}
	}
}
