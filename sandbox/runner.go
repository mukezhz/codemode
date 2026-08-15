// Package sandbox runs model-authored programs against a code mode runtime.
//
// A program receives a single host binding, codemode.execute, and composes
// capability calls through it. Search stays on the host: the program is told
// which capabilities exist by its prompt, never by the sandbox.
//
// The engine that isolates the program sits behind Runner, so the isolation
// level is a deployment choice rather than a code change. The bridge shape is
// the same for every engine: one (name, input) -> output call back to the host.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mukezhz/code-mode/codemode"
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

	// Name identifies the engine and its isolation level, for logs and for
	// telling an operator what is actually enforcing the boundary.
	Name() string

	// Close releases engine resources.
	Close() error
}

// Options bounds a single program run.
type Options struct {
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

// checked validates options and applies defaults.
func (options Options) checked() (Options, error) {
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

// New builds the runner named by SANDBOX_ENGINE. Only "goja" is implemented;
// it is the default. Options given here win over the environment, so a caller
// that sets a field explicitly is not overridden by deployment configuration.
//
// Environment:
//
//	SANDBOX_ENGINE     goja                      (default goja)
//	SANDBOX_TIMEOUT    Go duration, e.g. 10s     (default 30s)
//	SANDBOX_MAX_CALLS  capability call budget    (default 50)
func New(options Options) (Runner, error) {
	options, err := options.fromEnv()
	if err != nil {
		return nil, err
	}
	engine := strings.ToLower(strings.TrimSpace(os.Getenv("SANDBOX_ENGINE")))
	switch engine {
	case "goja", "":
		return NewGoja(options)
	default:
		return nil, fmt.Errorf("SANDBOX_ENGINE %q is not supported: use goja", engine)
	}
}

// fromEnv fills zero fields from the environment, leaving set fields alone.
func (options Options) fromEnv() (Options, error) {
	if options.Timeout == 0 {
		if value := strings.TrimSpace(os.Getenv("SANDBOX_TIMEOUT")); value != "" {
			timeout, err := time.ParseDuration(value)
			if err != nil {
				return options, fmt.Errorf("SANDBOX_TIMEOUT: %w", err)
			}
			options.Timeout = timeout
		}
	}
	if options.MaxCalls == 0 {
		if value := strings.TrimSpace(os.Getenv("SANDBOX_MAX_CALLS")); value != "" {
			maxCalls, err := strconv.Atoi(value)
			if err != nil {
				return options, fmt.Errorf("SANDBOX_MAX_CALLS: %w", err)
			}
			options.MaxCalls = maxCalls
		}
	}
	return options, nil
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
