package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dop251/goja"

	"github.com/mukezhz/code-mode/capability"
	"github.com/mukezhz/code-mode/codemode"
)

// gojaRunner evaluates programs in an in-process JavaScript interpreter.
//
// ponytail: goja is not a security boundary. It denies ambient capabilities
// (no require, no fetch, no filesystem) and bounds time and call count, which
// is enough for code this host asked a model to write. Implement Runner over a
// subprocess or container before running code supplied by end users; the bridge
// shape does not change, only the transport.
type gojaRunner struct {
	options Options
}

// NewGoja returns a Runner backed by the in-process goja interpreter.
func NewGoja(options Options) (Runner, error) {
	options, err := options.checked()
	if err != nil {
		return nil, err
	}
	return &gojaRunner{options: options}, nil
}

// javascriptTypes renders registry types as TypeScript. Bytes are shown as a
// base64 string rather than Uint8Array because that is what actually arrives:
// the bridge decodes protojson, which encodes bytes as base64 text.
var javascriptTypes = types{
	scalar: map[string]string{
		"bool":  "boolean",
		"bytes": "string (base64)",
	},
	list: func(element string) string { return element + "[]" },
	dict: func(key, value string) string { return "Record<" + key + ", " + value + ">" },
}

// javascript is the dialect goja evaluates.
var javascript = dialect{
	id:    "javascript",
	prose: "JavaScript",
	rules: []string{
		"Reply with the body of a function: statements only, no wrapper, no imports, no exports.",
		"Call capabilities as codemode.execute(name, input). It is synchronous and returns the decoded response object. Never use async, await, promises, or callbacks.",
		"End with a return statement whose value answers the request. Prefer a short human-readable string.",
	},
	example: `const { todos } = codemode.execute("todos.list", {});
const pending = todos.filter(todo => !todo.completed);
if (pending.length === 0) {
	return "nothing to do";
}
codemode.execute("todos.complete", { id: pending[0].id });
return "completed " + pending[0].title;`,
	comment: func(description string) string { return "/** " + description + " */" },
	signature: func(item capability.Capability) string {
		object := func(schema capability.TypeSchema) string {
			return fields(schema, javascriptTypes, func(name, fieldType string) string {
				return name + ": " + fieldType
			}, "{}", "{ ", " }")
		}
		return fmt.Sprintf("codemode.execute(name: %q, input: %s): %s;",
			item.Name, object(item.Input), object(item.Output))
	},
}

func (runner *gojaRunner) Language() string { return javascript.id }

func (runner *gojaRunner) Prompt(matches []capability.Match) string {
	return javascript.prompt(matches)
}

func (runner *gojaRunner) Name() string {
	return fmt.Sprintf("goja / JavaScript (in-process, %s timeout, %d call budget)",
		runner.options.Timeout, runner.options.MaxCalls)
}

// Close is a no-op: the interpreter is created per run and owns no resource
// that outlives it.
func (runner *gojaRunner) Close() error { return nil }

func (runner *gojaRunner) Run(ctx context.Context, mode codemode.CodeMode, source string) (any, error) {
	if mode == nil {
		return nil, errors.New("code mode runtime is required")
	}

	ctx, cancel := context.WithTimeout(ctx, runner.options.Timeout)
	defer cancel()

	// A fresh interpreter per run. Its globals are exactly what Set installs,
	// so the program has no filesystem, network, or module access.
	vm := goja.New()
	calls := 0

	// The entire host bridge. The capability name is an argument rather than a
	// binding, so this stays one function no matter how large the registry is.
	execute := func(name string, input map[string]any) any {
		calls++
		if calls > runner.options.MaxCalls {
			panic(vm.NewGoError(fmt.Errorf("call budget of %d exhausted", runner.options.MaxCalls)))
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
