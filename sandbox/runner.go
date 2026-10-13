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

type Runner interface {
	Run(ctx context.Context, mode codemode.CodeMode, source string) (any, error)

	Language() string

	Prompt(matches []capability.Match) string

	Name() string

	Close() error
}

type Options struct {
	Engine string

	Timeout  time.Duration
	MaxCalls int
	// ponytail: bounds input, not interpreter heap; MaxHeapBytes is the hard cap, subprocess engines only.
	MaxResponseBytes int

	// Worker is the argv that starts codemode-worker; subprocess engines only.
	Worker []string
	// WorkerIsolated says Worker starts a container that isolates itself, so the
	// host must not unshare namespaces around the container CLI.
	WorkerIsolated bool
	MaxHeapBytes   int
}

const (
	DefaultMaxCalls         = 50
	DefaultTimeout          = 30 * time.Second
	DefaultMaxResponseBytes = 1 << 20
	DefaultMaxHeapBytes     = 256 << 20
)

func (options Options) Checked() (Options, error) {
	if options.Timeout < 0 {
		return options, errors.New("timeout must not be negative")
	}
	if options.MaxCalls < 0 {
		return options, errors.New("max calls must not be negative")
	}
	if options.MaxResponseBytes < 0 {
		return options, errors.New("max response bytes must not be negative")
	}
	if options.MaxHeapBytes < 0 {
		return options, errors.New("max heap bytes must not be negative")
	}
	if options.Timeout == 0 {
		options.Timeout = DefaultTimeout
	}
	if options.MaxCalls == 0 {
		options.MaxCalls = DefaultMaxCalls
	}
	if options.MaxResponseBytes == 0 {
		options.MaxResponseBytes = DefaultMaxResponseBytes
	}
	if options.MaxHeapBytes == 0 {
		options.MaxHeapBytes = DefaultMaxHeapBytes
	}
	return options, nil
}

type Engine struct {
	Name    string
	Aliases []string
	New     func(Options) (Runner, error)
}

const (
	EngineGoja     = "goja"
	EngineStarlark = "starlark"
	EngineLua      = "lua"
)

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

func Run(ctx context.Context, mode codemode.CodeMode, source string, options Options) (any, error) {
	runner, err := New(options)
	if err != nil {
		return nil, err
	}
	defer runner.Close()
	return runner.Run(ctx, mode, source)
}

func (options Options) CheckResponse(name string, response []byte) error {
	if len(response) > options.MaxResponseBytes {
		return fmt.Errorf("response of %s is %d bytes, over the %d byte limit", name, len(response), options.MaxResponseBytes)
	}
	return nil
}
