package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
	"go.starlark.net/syntax"

	"github.com/mukezhz/code-mode/capability"
	"github.com/mukezhz/code-mode/codemode"
)

// starlarkRunner evaluates programs in Starlark, a small Python dialect built
// for configuration. It has no ambient I/O at all: the interpreter ships
// without file, network, or clock builtins, so the sandbox is subtractive by
// construction rather than by removal. Recursion and while loops are disallowed
// by default, which also bounds a class of runaway program.
//
// ponytail: like goja, this runs in-process and is not a defence against a
// determined attacker sharing the process.
type starlarkRunner struct {
	options Options
}

// NewStarlark returns a Runner backed by the in-process Starlark interpreter.
func NewStarlark(options Options) (Runner, error) {
	options, err := options.checked()
	if err != nil {
		return nil, err
	}
	return &starlarkRunner{options: options}, nil
}

// starlarkTypes renders registry types as Starlark values. Numbers stay open
// between int and float because the registry collapses every protobuf numeric
// scalar into one, and Starlark keeps the two apart.
var starlarkTypes = types{
	scalar: map[string]string{
		"number": "int|float",
		"bytes":  "string (base64)",
	},
	list: func(element string) string { return "[" + element + "]" },
	dict: func(key, value string) string { return "{" + key + ": " + value + "}" },
}

// starlarkDialect is the dialect the Starlark engine evaluates.
var starlarkDialect = dialect{
	id:    "starlark",
	prose: "Starlark (a small Python dialect)",
	rules: []string{
		"Reply with the body of a function: statements only, indented at column zero, no def line, no imports.",
		"Call capabilities as codemode.execute(name, input). It is synchronous and returns the decoded response as a dict.",
		"End with a return statement whose value answers the request. Prefer a short human-readable string.",
		"Starlark has no while loops and no recursion. Use for loops and list comprehensions.",
		"String concatenation uses +; convert numbers with str() first.",
	},
	example: `todos = codemode.execute("todos.list", {})["todos"]
pending = [todo for todo in todos if not todo["completed"]]
if len(pending) == 0:
    return "nothing to do"
codemode.execute("todos.complete", {"id": pending[0]["id"]})
return "completed " + pending[0]["title"]`,
	comment: func(description string) string { return "# " + description },
	signature: func(item capability.Capability) string {
		object := func(schema capability.TypeSchema) string {
			return fields(schema, starlarkTypes, func(name, fieldType string) string {
				return fmt.Sprintf("%q: %s", name, fieldType)
			}, "{}", "{", "}")
		}
		return fmt.Sprintf("codemode.execute(%q, %s) -> %s",
			item.Name, object(item.Input), object(item.Output))
	},
}

func (runner *starlarkRunner) Language() string { return starlarkDialect.id }

func (runner *starlarkRunner) Prompt(matches []capability.Match) string {
	return starlarkDialect.prompt(matches)
}

func (runner *starlarkRunner) Name() string {
	return fmt.Sprintf("starlark / Python-like (in-process, %s timeout, %d call budget)",
		runner.options.Timeout, runner.options.MaxCalls)
}

// Close is a no-op: the interpreter is created per run.
func (runner *starlarkRunner) Close() error { return nil }

func (runner *starlarkRunner) Run(ctx context.Context, mode codemode.CodeMode, source string) (any, error) {
	if mode == nil {
		return nil, errors.New("code mode runtime is required")
	}

	ctx, cancel := context.WithTimeout(ctx, runner.options.Timeout)
	defer cancel()

	thread := &starlark.Thread{Name: "codemode"}
	// Printing is the one builtin Starlark keeps; drop it so the program has no
	// output channel other than its return value.
	thread.Print = func(*starlark.Thread, string) {}

	calls := 0
	execute := func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var name string
		var input starlark.Value
		if err := starlark.UnpackPositionalArgs("execute", args, kwargs, 1, &name, &input); err != nil {
			return nil, err
		}
		calls++
		if calls > runner.options.MaxCalls {
			return nil, fmt.Errorf("call budget of %d exhausted", runner.options.MaxCalls)
		}

		request := json.RawMessage("{}")
		if input != nil && input != starlark.None {
			decoded, err := fromStarlark(input)
			if err != nil {
				return nil, fmt.Errorf("encode input for %s: %w", name, err)
			}
			if request, err = json.Marshal(decoded); err != nil {
				return nil, fmt.Errorf("encode input for %s: %w", name, err)
			}
		}

		response, err := mode.Execute(ctx, name, request)
		if err != nil {
			return nil, err
		}
		var output any
		if err := json.Unmarshal(response, &output); err != nil {
			return nil, fmt.Errorf("decode output of %s: %w", name, err)
		}
		return toStarlark(output)
	}

	// A struct, not a dict: only a struct supports codemode.execute attribute
	// access, which is what keeps the call spelled the same in every dialect.
	api := starlarkstruct.FromStringDict(starlarkstruct.Default, starlark.StringDict{
		"execute": starlark.NewBuiltin("execute", execute),
	})

	// Starlark has no top-level return, so the program body becomes the body of
	// a function this host defines and calls.
	program := "def __main():\n" + indentLines(source) + "\n__result = __main()\n"

	stop := context.AfterFunc(ctx, func() { thread.Cancel(ctx.Err().Error()) })
	defer stop()

	// The zero FileOptions is the restrictive one: no while loops, no
	// recursion, no top-level control flow. That matches the prompt's rules and
	// removes a class of runaway program at the language level.
	globals, err := starlark.ExecFileOptions(
		&syntax.FileOptions{},
		thread,
		"program.star",
		program,
		starlark.StringDict{"codemode": api},
	)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("sandbox stopped: %w", ctx.Err())
		}
		var evalErr *starlark.EvalError
		if errors.As(err, &evalErr) {
			return nil, fmt.Errorf("sandbox: %s", evalErr.Msg)
		}
		return nil, fmt.Errorf("sandbox: %w", err)
	}

	result, ok := globals["__result"]
	if !ok || result == starlark.None {
		return nil, nil
	}
	return fromStarlark(result)
}

// indentLines indents every line by four spaces so the model can write a body
// at column zero in an indentation-sensitive language.
func indentLines(source string) string {
	lines := strings.Split(strings.TrimRight(source, "\n"), "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			lines[i] = ""
			continue
		}
		lines[i] = "    " + line
	}
	return strings.Join(lines, "\n")
}

// toStarlark converts a JSON-decoded Go value into a Starlark value.
func toStarlark(value any) (starlark.Value, error) {
	switch typed := value.(type) {
	case nil:
		return starlark.None, nil
	case bool:
		return starlark.Bool(typed), nil
	case float64:
		if typed == float64(int64(typed)) {
			return starlark.MakeInt64(int64(typed)), nil
		}
		return starlark.Float(typed), nil
	case string:
		return starlark.String(typed), nil
	case []any:
		items := make([]starlark.Value, len(typed))
		for i, item := range typed {
			converted, err := toStarlark(item)
			if err != nil {
				return nil, err
			}
			items[i] = converted
		}
		return starlark.NewList(items), nil
	case map[string]any:
		dict := starlark.NewDict(len(typed))
		for key, item := range typed {
			converted, err := toStarlark(item)
			if err != nil {
				return nil, err
			}
			if err := dict.SetKey(starlark.String(key), converted); err != nil {
				return nil, err
			}
		}
		return dict, nil
	default:
		return nil, fmt.Errorf("cannot represent %T in starlark", value)
	}
}

// fromStarlark converts a Starlark value into a JSON-encodable Go value.
func fromStarlark(value starlark.Value) (any, error) {
	switch typed := value.(type) {
	case starlark.NoneType:
		return nil, nil
	case starlark.Bool:
		return bool(typed), nil
	case starlark.Int:
		number, ok := typed.Int64()
		if !ok {
			return nil, errors.New("integer is out of range")
		}
		return number, nil
	case starlark.Float:
		return float64(typed), nil
	case starlark.String:
		return string(typed), nil
	case *starlark.List:
		items := make([]any, typed.Len())
		for i := range items {
			converted, err := fromStarlark(typed.Index(i))
			if err != nil {
				return nil, err
			}
			items[i] = converted
		}
		return items, nil
	case starlark.Tuple:
		items := make([]any, typed.Len())
		for i := range items {
			converted, err := fromStarlark(typed.Index(i))
			if err != nil {
				return nil, err
			}
			items[i] = converted
		}
		return items, nil
	case *starlark.Dict:
		result := make(map[string]any, typed.Len())
		for _, item := range typed.Items() {
			key, ok := starlark.AsString(item[0])
			if !ok {
				return nil, fmt.Errorf("dict key %s is not a string", item[0].String())
			}
			converted, err := fromStarlark(item[1])
			if err != nil {
				return nil, err
			}
			result[key] = converted
		}
		return result, nil
	default:
		return nil, fmt.Errorf("cannot represent starlark %s in Go", value.Type())
	}
}
