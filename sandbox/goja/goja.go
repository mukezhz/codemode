package goja

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	gojavm "github.com/dop251/goja"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
	"github.com/mukezhz/codemode/sandbox"
	"github.com/mukezhz/codemode/sandbox/internal/prompt"
)

func init() {
	sandbox.RegisterEngine(sandbox.Engine{
		Name:    sandbox.EngineGoja,
		Aliases: []string{"javascript", "js"},
		New:     New,
	})
}

// gojaRunner evaluates JavaScript in-process; it denies ambient capabilities but is not a security boundary for end-user code.
type gojaRunner struct {
	options sandbox.Options
}

var _ sandbox.Runner = (*gojaRunner)(nil)

// New returns a Runner backed by the in-process goja interpreter.
func New(options sandbox.Options) (sandbox.Runner, error) {
	options, err := options.Checked()
	if err != nil {
		return nil, err
	}
	return &gojaRunner{options: options}, nil
}

// javascriptTypes renders registry types as TypeScript; bytes show as base64 strings because protojson encodes them so.
var javascriptTypes = prompt.Types{
	Scalar: map[string]string{
		"bool":  "boolean",
		"bytes": "string (base64)",
	},
	List: func(element string) string { return element + "[]" },
	Dict: func(key, value string) string { return "Record<" + key + ", " + value + ">" },
}

// javascript is the dialect goja evaluates.
var javascript = prompt.Dialect{
	ID:    "javascript",
	Prose: "JavaScript",
	Rules: []string{
		"Reply with the body of a function: statements only, no wrapper, no imports, no exports.",
		"Call capabilities as codemode.execute(name, input). It is synchronous and returns the decoded response object. Never use async, await, promises, or callbacks.",
		"End with a return statement whose value answers the request. Prefer a short human-readable string.",
	},
	Example: `const { todos } = codemode.execute("todos.list", {});
const pending = todos.filter(todo => !todo.completed);
if (pending.length === 0) {
	return "nothing to do";
}
codemode.execute("todos.complete", { id: pending[0].id });
return "completed " + pending[0].title;`,
	Comment: func(description string) string { return "/** " + description + " */" },
	Shape: func(name string, schema capability.TypeSchema) string {
		return "type " + name + " = " + prompt.Fields(schema, javascriptTypes,
			func(field, fieldType string) string { return field + ": " + fieldType },
			"{}", "{ ", " }") + ";"
	},
	Signature: func(item capability.Capability) string {
		object := func(schema capability.TypeSchema) string {
			return prompt.Fields(schema, javascriptTypes, func(name, fieldType string) string {
				return name + ": " + fieldType
			}, "{}", "{ ", " }")
		}
		return fmt.Sprintf("codemode.execute(name: %q, input: %s): %s;",
			item.Name, object(item.Input), object(item.Output))
	},
}

func (runner *gojaRunner) Language() string { return javascript.ID }

func (runner *gojaRunner) Prompt(matches []capability.Match) string {
	return javascript.Render(matches)
}

func (runner *gojaRunner) Name() string {
	return fmt.Sprintf("goja / JavaScript (in-process, %s timeout, %d call budget)",
		runner.options.Timeout, runner.options.MaxCalls)
}

// Close is a no-op: the interpreter is created per run.
func (runner *gojaRunner) Close() error { return nil }

func (runner *gojaRunner) Run(ctx context.Context, mode codemode.CodeMode, source string) (any, error) {
	if mode == nil {
		return nil, errors.New("code mode runtime is required")
	}

	ctx, cancel := context.WithTimeout(ctx, runner.options.Timeout)
	defer cancel()

	// Fresh interpreter per run; its only globals are what Set installs.
	vm := gojavm.New()
	calls := 0

	// The entire host bridge; the capability name is an argument, not a binding.
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
		var interrupted *gojavm.InterruptedError
		if errors.As(err, &interrupted) {
			return nil, fmt.Errorf("sandbox stopped: %w", ctx.Err())
		}
		return nil, fmt.Errorf("sandbox: %w", err)
	}
	if value == nil || gojavm.IsUndefined(value) || gojavm.IsNull(value) {
		return nil, nil
	}
	return value.Export(), nil
}
