package sandbox

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
)

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
		runner, err := New(Options{Engine: name})
		if err != nil {
			t.Fatalf("New(%q) returned error: %v", name, err)
		}
		if runner.Language() != "stub" {
			t.Fatalf("Language() = %q, want %q", runner.Language(), "stub")
		}
	}
}

func TestNewNamesTheMissingImportForAShippedEngine(t *testing.T) {
	_, err := New(Options{Engine: EngineLua})
	if err == nil {
		t.Fatal("New returned no error for an unregistered engine")
	}
	if !strings.Contains(err.Error(), "sandbox/lua") {
		t.Fatalf("error = %v, want it to name the import path", err)
	}
}

func TestNewRejectsAnUnknownEngine(t *testing.T) {
	_, err := New(Options{Engine: "wasm"})
	if err == nil {
		t.Fatal("New returned no error for an unknown engine")
	}
	if strings.Contains(err.Error(), "sandbox/wasm") {
		t.Fatalf("error = %v, want it to not invent an import path", err)
	}
}

func TestNewIgnoresTheEnvironment(t *testing.T) {
	t.Setenv(EnvEngine, "stub")
	t.Setenv(EnvTimeout, "7s")

	if _, err := New(Options{}); err == nil {
		t.Fatal("New succeeded — it read SANDBOX_ENGINE instead of defaulting")
	}

	runner, err := New(Options{Engine: "stub"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(runner.Name(), "7s") {
		t.Fatalf("Name() = %q — New read SANDBOX_TIMEOUT", runner.Name())
	}
}

func TestNewFromEnvFillsOnlyZeroFields(t *testing.T) {
	t.Setenv(EnvEngine, "stub")
	t.Setenv(EnvTimeout, "7s")

	runner, err := NewFromEnv(Options{})
	if err != nil {
		t.Fatalf("NewFromEnv returned error: %v", err)
	}
	if !strings.Contains(runner.Name(), "7s") {
		t.Fatalf("Name() = %q, want the environment timeout", runner.Name())
	}

	explicit, err := NewFromEnv(Options{Timeout: 3 * time.Second})
	if err != nil {
		t.Fatalf("NewFromEnv returned error: %v", err)
	}
	if !strings.Contains(explicit.Name(), "3s") {
		t.Fatalf("Name() = %q, want the explicit timeout to win", explicit.Name())
	}
}

func TestNewFromEnvRejectsUnparseableBounds(t *testing.T) {
	t.Setenv(EnvEngine, "stub")
	t.Setenv(EnvTimeout, "soon")
	if _, err := NewFromEnv(Options{}); err == nil {
		t.Fatalf("NewFromEnv returned no error for an unparseable %s", EnvTimeout)
	}
}

func TestRegisteredEnginesListsCanonicalNamesOnly(t *testing.T) {
	for _, name := range RegisteredEngines() {
		if name == "stubby" {
			t.Fatal("RegisteredEngines listed an alias")
		}
	}
}
