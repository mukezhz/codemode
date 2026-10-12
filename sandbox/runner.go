// Package sandbox runs model-authored programs whose only host binding is
// codemode.execute; engines live in subpackages and register from init.
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

// Runner evaluates untrusted, model-authored programs; every engine exposes
// only codemode.execute.
type Runner interface {
	// Run evaluates untrusted source with codemode.execute bound to mode and
	// returns the program's final value decoded into Go types.
	Run(ctx context.Context, mode codemode.CodeMode, source string) (any, error)

	// Language names the dialect programs must be written in.
	Language() string

	// Prompt renders the system prompt for this engine with the declarations for matches.
	Prompt(matches []capability.Match) string

	// Name identifies the engine and its isolation level.
	Name() string

	// Close releases engine resources.
	Close() error
}

// Options selects an engine and bounds a single program run.
type Options struct {
	// Engine names the interpreter; empty applies DefaultEngine.
	Engine string

	// Timeout bounds the run; zero applies DefaultTimeout.
	Timeout time.Duration
	// MaxCalls limits capability calls per run; zero applies DefaultMaxCalls.
	MaxCalls int
}

// Defaults applied when the matching Options field is zero.
const (
	DefaultMaxCalls = 50
	DefaultTimeout  = 30 * time.Second
)

// Checked validates options and applies defaults.
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
	// Name is the canonical name that selects it.
	Name string
	// Aliases are other accepted spellings.
	Aliases []string
	// New builds a Runner bounded by options.
	New func(Options) (Runner, error)
}

// Engines this module ships; blank-import the matching package to register one.
const (
	// EngineGoja evaluates JavaScript (sandbox/goja).
	EngineGoja = "goja"
	// EngineStarlark evaluates Starlark (sandbox/starlark).
	EngineStarlark = "starlark"
	// EngineLua evaluates Lua 5.1 (sandbox/lua).
	EngineLua = "lua"
)

// DefaultEngine is used when Options.Engine is empty.
const DefaultEngine = EngineGoja

// shipped maps each built-in engine to its package, so errors can name the missing import.
var shipped = map[string]string{
	EngineGoja:     "github.com/mukezhz/codemode/sandbox/goja",
	EngineStarlark: "github.com/mukezhz/codemode/sandbox/starlark",
	EngineLua:      "github.com/mukezhz/codemode/sandbox/lua",
}

var (
	enginesMutex sync.RWMutex
	engines      = map[string]Engine{}
)

// RegisterEngine makes engine selectable by New; registering a name twice panics.
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

// RegisteredEngines lists the canonical names of every registered engine, sorted.
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

// unregisteredEngine distinguishes a missing import from an unknown engine.
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

// Run evaluates source on a one-shot runner built from options.
func Run(ctx context.Context, mode codemode.CodeMode, source string, options Options) (any, error) {
	runner, err := New(options)
	if err != nil {
		return nil, err
	}
	defer runner.Close()
	return runner.Run(ctx, mode, source)
}
