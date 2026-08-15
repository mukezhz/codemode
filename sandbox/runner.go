// Package sandbox runs model-authored programs against a code mode runtime.
//
// A program receives a single host binding, codemode.execute, and composes
// capability calls through it. Search stays on the host: the program is told
// which capabilities exist by its prompt, never by the sandbox.
//
// The engine that isolates the program sits behind Runner, so the isolation
// level is a deployment choice rather than a code change. The bridge shape is
// the same for every engine: one (name, input) -> output call back to the host.
//
// # Configuration
//
// Everything is configured through sandbox.Options. This package reads no
// environment variable of its own; OptionsFromEnv and NewFromEnv do, and only
// when you call them.
//
// # Choosing an engine
//
// This package holds the interface, not the interpreters. Blank-import the
// engines you want and they register themselves, so a binary links only the
// interpreters it can actually run:
//
//	import (
//		"github.com/mukezhz/codemode/sandbox"
//		_ "github.com/mukezhz/codemode/sandbox/goja"
//	)
//
//	runner, err := sandbox.New(sandbox.Options{Engine: sandbox.EngineGoja})
//
// Importing an engine directly and calling its own New is the most explicit
// form, and needs no registration at all.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
)

// Runner evaluates untrusted, model-authored programs.
//
// Implementations differ in how strongly they isolate the program, not in what
// it can reach: every engine exposes codemode.execute and nothing else, and
// every capability call is authorized by the host's registry regardless of the
// engine in use.
type Runner interface {
	// Run evaluates source with codemode.execute bound to mode, and returns
	// the program's final value decoded into Go types. Ctx carries
	// cancellation; source is untrusted.
	Run(ctx context.Context, mode codemode.CodeMode, source string) (any, error)

	// Language names the dialect programs must be written in, such as
	// "javascript".
	Language() string

	// Prompt renders the system prompt for this engine: how to write a program
	// it can evaluate, followed by the declarations for matches. The engine
	// that runs the program owns the instructions for writing it, so a caller
	// never has to know which dialect is configured.
	Prompt(matches []capability.Match) string

	// Name identifies the engine and its isolation level, for logs and for
	// telling an operator what is actually enforcing the boundary.
	Name() string

	// Close releases engine resources.
	Close() error
}

// Options selects an engine and bounds a single program run.
type Options struct {
	// Engine names the interpreter: goja, starlark, or lua. Empty applies
	// DefaultEngine.
	Engine string

	// Timeout stops the program once it has run this long. Zero applies
	// DefaultTimeout.
	Timeout time.Duration
	// MaxCalls limits how many capability calls the program may make.
	// Zero applies DefaultMaxCalls.
	MaxCalls int
}

// Defaults applied when the matching Options field is zero.
const (
	DefaultMaxCalls = 50
	DefaultTimeout  = 30 * time.Second
)

// Checked validates options and applies defaults. Engine constructors call it
// so every engine bounds a run the same way.
func (options Options) Checked() (Options, error) {
	if options.Timeout < 0 {
		return options, errors.New("timeout must not be negative")
	}
	if options.MaxCalls < 0 {
		return options, errors.New("max calls must not be negative")
	}
	if options.Timeout == 0 {
		options.Timeout = DefaultTimeout
	}
	if options.MaxCalls == 0 {
		options.MaxCalls = DefaultMaxCalls
	}
	return options, nil
}

// Engine describes one registered interpreter.
type Engine struct {
	// Name is the canonical name that selects it, such as "goja".
	Name string
	// Aliases are other accepted spellings, such as "javascript" for goja.
	Aliases []string
	// New builds a Runner bounded by options.
	New func(Options) (Runner, error)
}

// Engines are the dialects this module ships. Each is a pure Go interpreter
// with no ambient I/O, and each exposes the same single binding, so a capability
// call looks the same in all of them. Blank-import the matching package to make
// one available.
const (
	// EngineGoja evaluates JavaScript. The most familiar dialect to a model,
	// and the largest language surface. Package sandbox/goja.
	EngineGoja = "goja"
	// EngineStarlark evaluates Starlark, a small Python dialect that is
	// deterministic and has no unbounded recursion or ambient state.
	// Package sandbox/starlark.
	EngineStarlark = "starlark"
	// EngineLua evaluates Lua 5.1, the smallest and fastest of the three.
	// Package sandbox/lua.
	EngineLua = "lua"
)

// DefaultEngine is used when Options.Engine is empty.
const DefaultEngine = EngineGoja

// shipped maps an engine this module provides to the package that registers it,
// so an unregistered name produces an error naming the missing import rather
// than a bare "unsupported".
var shipped = map[string]string{
	EngineGoja:     "github.com/mukezhz/codemode/sandbox/goja",
	EngineStarlark: "github.com/mukezhz/codemode/sandbox/starlark",
	EngineLua:      "github.com/mukezhz/codemode/sandbox/lua",
}

var (
	enginesMutex sync.RWMutex
	engines      = map[string]Engine{}
)

// RegisterEngine makes engine selectable by New. Engine packages call it from
// init, so blank-importing one is enough. Registering a name twice panics,
// which catches a duplicate import path at startup rather than in production.
func RegisterEngine(engine Engine) {
	if engine.Name == "" || engine.New == nil {
		panic("sandbox: engine needs a name and a constructor")
	}
	enginesMutex.Lock()
	defer enginesMutex.Unlock()
	for _, name := range append([]string{engine.Name}, engine.Aliases...) {
		if _, exists := engines[name]; exists {
			panic("sandbox: engine " + name + " is already registered")
		}
		engines[name] = engine
	}
}

// RegisteredEngines lists the canonical names of every registered engine,
// sorted. Useful in diagnostics and startup logs.
func RegisteredEngines() []string {
	enginesMutex.RLock()
	defer enginesMutex.RUnlock()
	unique := map[string]bool{}
	for _, engine := range engines {
		unique[engine.Name] = true
	}
	names := make([]string, 0, len(unique))
	for name := range unique {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// New builds the runner named by options.Engine from the registered engines.
// It reads no environment variable; use OptionsFromEnv first if you want one
// consulted.
func New(options Options) (Runner, error) {
	name := strings.ToLower(strings.TrimSpace(options.Engine))
	if name == "" {
		name = DefaultEngine
	}

	enginesMutex.RLock()
	engine, registered := engines[name]
	enginesMutex.RUnlock()
	if !registered {
		return nil, unregisteredEngine(name)
	}
	return engine.New(options)
}

// unregisteredEngine distinguishes "you forgot the import" from "no such
// engine", because the fix is different and the first is far more common.
func unregisteredEngine(name string) error {
	if path, ships := shipped[name]; ships {
		return fmt.Errorf("sandbox engine %q is not registered: add `_ %q` to your imports", name, path)
	}
	available := RegisteredEngines()
	if len(available) == 0 {
		return fmt.Errorf("sandbox engine %q is unknown and no engines are registered: blank-import one of %s",
			name, strings.Join(values(shipped), ", "))
	}
	return fmt.Errorf("sandbox engine %q is unknown: registered engines are %s",
		name, strings.Join(available, ", "))
}

func values(paths map[string]string) []string {
	list := make([]string, 0, len(paths))
	for _, path := range paths {
		list = append(list, path)
	}
	sort.Strings(list)
	return list
}

// Run evaluates source on a runner built from options. It is the one-shot form
// of New followed by Runner.Run, for callers that do not keep a runner around.
func Run(ctx context.Context, mode codemode.CodeMode, source string, options Options) (any, error) {
	runner, err := New(options)
	if err != nil {
		return nil, err
	}
	defer runner.Close()
	return runner.Run(ctx, mode, source)
}
