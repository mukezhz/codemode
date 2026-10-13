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
	// Engine is the language: goja, starlark or lua.
	Engine string
	// Isolation is where programs run: IsolationInProcess (default) or a registered one such as IsolationProcess.
	Isolation string

	Timeout  time.Duration
	MaxCalls int
	// ponytail: bounds input, not interpreter heap; MaxHeapBytes is the hard cap, subprocess engines only.
	MaxResponseBytes int

	// Worker is the argv that starts codemode-worker; process isolation only.
	Worker []string
	// WorkerContainer says Worker starts a container that isolates itself, so the
	// host must not unshare namespaces around the container CLI.
	WorkerContainer bool
	MaxHeapBytes    int
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

const (
	IsolationInProcess = "inprocess"
	IsolationProcess   = "process"
)

// Isolation runs an engine somewhere other than this process. New receives the
// options with Engine still naming the language.
type Isolation struct {
	Name string
	New  func(Options) (Runner, error)
}

var shippedIsolations = map[string]string{
	IsolationProcess: "github.com/mukezhz/codemode/sandbox/subprocess",
}

var isolations = map[string]Isolation{}

// RegisterIsolation makes isolation selectable by New; registering a name twice panics.
func RegisterIsolation(isolation Isolation) {
	if isolation.Name == "" || isolation.Name == IsolationInProcess || isolation.New == nil {
		panic("sandbox: isolation needs a name other than inprocess and a constructor")
	}
	enginesMutex.Lock()
	defer enginesMutex.Unlock()
	if _, exists := isolations[isolation.Name]; exists {
		panic("sandbox: isolation " + isolation.Name + " is already registered")
	}
	isolations[isolation.Name] = isolation
}

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
	isolation := strings.ToLower(strings.TrimSpace(options.Isolation))
	if isolation != "" && isolation != IsolationInProcess {
		enginesMutex.RLock()
		registered, ok := isolations[isolation]
		enginesMutex.RUnlock()
		if !ok {
			return nil, unregisteredIsolation(isolation)
		}
		options.Isolation = isolation
		return registered.New(options)
	}
	// Worker settings with in-process isolation almost always mean SANDBOX_ISOLATION was forgotten,
	// and running in-process then is the unsafe way to be wrong.
	if len(options.Worker) > 0 || options.WorkerContainer {
		return nil, errors.New("a worker is configured but isolation is inprocess: set SANDBOX_ISOLATION=process, or remove SANDBOX_WORKER and SANDBOX_WORKER_CONTAINER")
	}

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

func unregisteredIsolation(name string) error {
	if path, ships := shippedIsolations[name]; ships {
		return fmt.Errorf("sandbox isolation %q is not registered: add `_ %q` to your imports", name, path)
	}
	known := []string{IsolationInProcess}
	enginesMutex.RLock()
	for registered := range isolations {
		known = append(known, registered)
	}
	enginesMutex.RUnlock()
	sort.Strings(known)
	return fmt.Errorf("sandbox isolation %q is unknown: use one of %s", name, strings.Join(known, ", "))
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
