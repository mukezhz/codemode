// Package sandbox runs model-authored programs against a code mode runtime.
//
// A program receives a single host binding, codemode.execute, and composes
// capability calls through it. Search stays on the host: the program is told
// which capabilities exist by its prompt, never by the sandbox.
package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dop251/goja"

	"github.com/mukezhz/code-mode/capability"
	"github.com/mukezhz/code-mode/codemode"
)

// Options bounds a single program run.
type Options struct {
	// Timeout stops the program once it has run this long. Required.
	Timeout time.Duration
	// MaxCalls limits how many capability calls the program may make.
	// Zero applies DefaultMaxCalls.
	MaxCalls int
}

// DefaultMaxCalls bounds capability calls when Options.MaxCalls is zero.
const DefaultMaxCalls = 50

// checked validates options and applies defaults.
func (options Options) checked() (Options, error) {
	if options.Timeout <= 0 {
		return options, errors.New("timeout must be positive")
	}
	if options.MaxCalls < 0 {
		return options, errors.New("max calls must not be negative")
	}
	if options.MaxCalls == 0 {
		options.MaxCalls = DefaultMaxCalls
	}
	return options, nil
}

// Run evaluates source with codemode.execute bound to mode and returns the
// program's final value decoded into Go types. Ctx carries cancellation and is
// passed to every capability call; source is the untrusted program body.
//
// ponytail: goja is an in-process interpreter, not a security boundary. It
// denies ambient capabilities (no require, no fetch, no filesystem) and bounds
// time and call count, which is enough for code this host asked a model to
// write. Move to a subprocess or container before running code supplied by
// end users.
func Run(ctx context.Context, mode codemode.CodeMode, source string, options Options) (any, error) {
	if mode == nil {
		return nil, errors.New("code mode runtime is required")
	}
	options, err := options.checked()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()

	// A fresh interpreter per run. Its globals are exactly what Set installs,
	// so the program has no filesystem, network, or module access.
	vm := goja.New()
	calls := 0

	// The entire host bridge. The capability name is an argument rather than a
	// binding, so this stays one function no matter how large the registry is.
	execute := func(name string, input map[string]any) any {
		calls++
		if calls > options.MaxCalls {
			panic(vm.NewGoError(fmt.Errorf("call budget of %d exhausted", options.MaxCalls)))
		}
		request, err := json.Marshal(input)
		if err != nil {
			panic(vm.NewGoError(fmt.Errorf("encode input for %s: %w", name, err)))
		}
		response, err := mode.Execute(ctx, name, request)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		var output any
		if err := json.Unmarshal(response, &output); err != nil {
			panic(vm.NewGoError(fmt.Errorf("decode output of %s: %w", name, err)))
		}
		return output
	}

	api := vm.NewObject()
	if err := api.Set("execute", execute); err != nil {
		return nil, err
	}
	if err := vm.Set("codemode", api); err != nil {
		return nil, err
	}

	stop := context.AfterFunc(ctx, func() { vm.Interrupt(ctx.Err()) })
	defer stop()

	value, err := vm.RunString("(function(){\n" + source + "\n})()")
	if err != nil {
		var interrupted *goja.InterruptedError
		if errors.As(err, &interrupted) {
			return nil, fmt.Errorf("sandbox stopped: %w", ctx.Err())
		}
		return nil, fmt.Errorf("sandbox: %w", err)
	}
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil, nil
	}
	return value.Export(), nil
}

// Declarations renders matches as TypeScript signatures for a model prompt.
// Only these capabilities are described to the model; the executable allowlist
// remains the full registry held by the code mode runtime.
func Declarations(matches []capability.Match) string {
	var builder strings.Builder
	for _, match := range matches {
		item := match.Capability
		fmt.Fprintf(&builder, "/** %s */\ncodemode.execute(name: %q, input: %s): %s;\n\n",
			item.Description, item.Name, object(item.Input), object(item.Output))
	}
	return builder.String()
}

func object(schema capability.TypeSchema) string {
	if len(schema.Fields) == 0 {
		return "{}"
	}
	fields := make([]string, 0, len(schema.Fields))
	for name, fieldType := range schema.Fields {
		fields = append(fields, name+": "+fieldType)
	}
	sort.Strings(fields) // A stable prompt keeps model responses cacheable.
	return "{ " + strings.Join(fields, "; ") + " }"
}
