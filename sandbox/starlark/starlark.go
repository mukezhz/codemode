package starlark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	starlarkvm "go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
	"go.starlark.net/syntax"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
	"github.com/mukezhz/codemode/sandbox"
	"github.com/mukezhz/codemode/sandbox/internal/prompt"
)

func init() {
	sandbox.RegisterEngine(sandbox.Engine{
		Name:    sandbox.EngineStarlark,
		Aliases: []string{"python", "py"},
		New:     New,
	})
}

// starlarkRunner evaluates Starlark in-process; it has no I/O builtins but is not a boundary against a determined attacker.
type starlarkRunner struct {
	options sandbox.Options
}

var _ sandbox.Runner = (*starlarkRunner)(nil)

// New returns a Runner backed by the in-process Starlark interpreter.
func New(options sandbox.Options) (sandbox.Runner, error) {
	options, err := options.Checked()
	if err != nil {
		return nil, err
	}
	return &starlarkRunner{options: options}, nil
}

// starlarkTypes renders registry types as Starlark; numbers stay int-or-float because the registry merges numeric scalars.
var starlarkTypes = prompt.Types{
	Scalar: map[string]string{
		"number": "int|float",
		"bytes":  "string (base64)",
	},
	List: func(element string) string { return "[" + element + "]" },
	Dict: func(key, value string) string { return "{" + key + ": " + value + "}" },
}

// starlarkDialect is the dialect the Starlark engine evaluates.
var starlarkDialect = prompt.Dialect{
	ID:    "starlark",
	Prose: "Starlark (a small Python dialect)",
	Rules: []string{
		"Reply with the body of a function: statements only, indented at column zero, no def line, no imports.",
		"Call capabilities as codemode.execute(name, input). It is synchronous and returns the decoded response as a dict.",
		"End with a return statement whose value answers the request. Prefer a short human-readable string.",
		"Starlark has no while loops and no recursion. Use for loops and list comprehensions.",
		"String concatenation uses +; convert numbers with str() first.",
	},
	Example: `todos = codemode.execute("todos.list", {})["todos"]
pending = [todo for todo in todos if not todo["completed"]]
if len(pending) == 0:
    return "nothing to do"
codemode.execute("todos.complete", {"id": pending[0]["id"]})
return "completed " + pending[0]["title"]`,
	Comment: func(description string) string { return "# " + description },
	Shape: func(name string, schema capability.TypeSchema) string {
		return "# " + name + " = " + prompt.Fields(schema, starlarkTypes,
			func(field, fieldType string) string { return fmt.Sprintf("%q: %s", field, fieldType) },
			"{}", "{", "}")
	},
	Signature: func(item capability.Capability) string {
		object := func(schema capability.TypeSchema) string {
			return prompt.Fields(schema, starlarkTypes, func(name, fieldType string) string {
				return fmt.Sprintf("%q: %s", name, fieldType)
			}, "{}", "{", "}")
		}
		return fmt.Sprintf("codemode.execute(%q, %s) -> %s",
			item.Name, object(item.Input), object(item.Output))
	},
}

func (runner *starlarkRunner) Language() string { return starlarkDialect.ID }

func (runner *starlarkRunner) Prompt(matches []capability.Match) string {
	return starlarkDialect.Render(matches)
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

	thread := &starlarkvm.Thread{Name: "codemode"}
	// Drop print so the return value is the only output channel.
	thread.Print = func(*starlarkvm.Thread, string) {}

	calls := 0
	execute := func(_ *starlarkvm.Thread, _ *starlarkvm.Builtin, args starlarkvm.Tuple, kwargs []starlarkvm.Tuple) (starlarkvm.Value, error) {
		var name string
		var input starlarkvm.Value
		if err := starlarkvm.UnpackPositionalArgs("execute", args, kwargs, 1, &name, &input); err != nil {
			return nil, err
		}
		calls++
		if calls > runner.options.MaxCalls {
			return nil, fmt.Errorf("call budget of %d exhausted", runner.options.MaxCalls)
		}

		request := json.RawMessage("{}")
		if input != nil && input != starlarkvm.None {
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

	// A struct, not a dict, so codemode.execute attribute access works.
	api := starlarkstruct.FromStringDict(starlarkstruct.Default, starlarkvm.StringDict{
		"execute": starlarkvm.NewBuiltin("execute", execute),
	})

	// Starlark has no top-level return, so wrap the body in a function.
	program := "def __main():\n" + indentLines(source) + "\n__result = __main()\n"

	stop := context.AfterFunc(ctx, func() { thread.Cancel(ctx.Err().Error()) })
	defer stop()

	// Zero FileOptions forbids while loops, recursion, and top-level control flow.
	globals, err := starlarkvm.ExecFileOptions(
		&syntax.FileOptions{},
		thread,
		"program.star",
		program,
		starlarkvm.StringDict{"codemode": api},
	)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("sandbox stopped: %w", ctx.Err())
		}
		var evalErr *starlarkvm.EvalError
		if errors.As(err, &evalErr) {
			return nil, fmt.Errorf("sandbox: %s", evalErr.Msg)
		}
		return nil, fmt.Errorf("sandbox: %w", err)
	}

	result, ok := globals["__result"]
	if !ok || result == starlarkvm.None {
		return nil, nil
	}
	return fromStarlark(result)
}

// indentLines indents every line by four spaces to form a function body.
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
func toStarlark(value any) (starlarkvm.Value, error) {
	switch typed := value.(type) {
	case nil:
		return starlarkvm.None, nil
	case bool:
		return starlarkvm.Bool(typed), nil
	case float64:
		if typed == float64(int64(typed)) {
			return starlarkvm.MakeInt64(int64(typed)), nil
		}
		return starlarkvm.Float(typed), nil
	case string:
		return starlarkvm.String(typed), nil
	case []any:
		items := make([]starlarkvm.Value, len(typed))
		for i, item := range typed {
			converted, err := toStarlark(item)
			if err != nil {
				return nil, err
			}
			items[i] = converted
		}
		return starlarkvm.NewList(items), nil
	case map[string]any:
		dict := starlarkvm.NewDict(len(typed))
		for key, item := range typed {
			converted, err := toStarlark(item)
			if err != nil {
				return nil, err
			}
			if err := dict.SetKey(starlarkvm.String(key), converted); err != nil {
				return nil, err
			}
		}
		return dict, nil
	default:
		return nil, fmt.Errorf("cannot represent %T in starlark", value)
	}
}

// fromStarlark converts a Starlark value into a JSON-encodable Go value.
func fromStarlark(value starlarkvm.Value) (any, error) {
	switch typed := value.(type) {
	case starlarkvm.NoneType:
		return nil, nil
	case starlarkvm.Bool:
		return bool(typed), nil
	case starlarkvm.Int:
		number, ok := typed.Int64()
		if !ok {
			return nil, errors.New("integer is out of range")
		}
		return number, nil
	case starlarkvm.Float:
		return float64(typed), nil
	case starlarkvm.String:
		return string(typed), nil
	case *starlarkvm.List:
		items := make([]any, typed.Len())
		for i := range items {
			converted, err := fromStarlark(typed.Index(i))
			if err != nil {
				return nil, err
			}
			items[i] = converted
		}
		return items, nil
	case starlarkvm.Tuple:
		items := make([]any, typed.Len())
		for i := range items {
			converted, err := fromStarlark(typed.Index(i))
			if err != nil {
				return nil, err
			}
			items[i] = converted
		}
		return items, nil
	case *starlarkvm.Dict:
		result := make(map[string]any, typed.Len())
		for _, item := range typed.Items() {
			key, ok := starlarkvm.AsString(item[0])
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
