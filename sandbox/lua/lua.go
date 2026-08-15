package lua

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	luavm "github.com/yuin/gopher-lua"

	"github.com/mukezhz/code-mode/capability"
	"github.com/mukezhz/code-mode/codemode"
	"github.com/mukezhz/code-mode/sandbox"
	"github.com/mukezhz/code-mode/sandbox/internal/prompt"
)

func init() {
	sandbox.RegisterEngine(sandbox.Engine{
		Name: sandbox.EngineLua,
		New:  New,
	})
}

// luaRunner evaluates programs in Lua 5.1. It is the smallest and fastest of
// the three engines. The standard library is opened selectively: base, table,
// string, and math, but not io, os, package, or debug, so the program has no
// file access, no clock, and no module loader.
//
// ponytail: like the others, this runs in-process and is not a defence against
// a determined attacker sharing the process.
type luaRunner struct {
	options sandbox.Options
}

// New returns a Runner backed by the in-process Lua interpreter. Blank-import
// this package instead to have sandbox.New select it from SANDBOX_ENGINE.
func New(options sandbox.Options) (sandbox.Runner, error) {
	options, err := options.Checked()
	if err != nil {
		return nil, err
	}
	return &luaRunner{options: options}, nil
}

// luaTypes renders registry types as Lua table constructors, which is the only
// shape Lua has for both lists and maps. The bracketed key form is real Lua, so
// a model reading it can copy the construction directly.
var luaTypes = prompt.Types{
	Scalar: map[string]string{
		"bool":  "boolean",
		"bytes": "string (base64)",
	},
	List: func(element string) string { return "{" + element + "}" },
	Dict: func(key, value string) string { return "{[" + key + "] = " + value + "}" },
}

// luaDialect is the dialect the Lua engine evaluates.
var luaDialect = prompt.Dialect{
	ID:    "lua",
	Prose: "Lua 5.1",
	Rules: []string{
		"Reply with statements at the top level of a chunk: no function wrapper, no require.",
		"Call capabilities as codemode.execute(name, input). It is synchronous and returns the decoded response as a table.",
		"End with a return statement whose value answers the request. Prefer a short human-readable string.",
		"Tables from a capability are arrays indexed from 1; use ipairs to walk them and # for length.",
		"String concatenation uses ..; convert numbers with tostring() first.",
		"Only the base, table, string, and math libraries exist. There is no io, os, or require.",
	},
	Example: `local todos = codemode.execute("todos.list", {}).todos
local pending = {}
for _, todo in ipairs(todos) do
  if not todo.completed then
    pending[#pending + 1] = todo
  end
end
if #pending == 0 then
  return "nothing to do"
end
codemode.execute("todos.complete", { id = pending[1].id })
return "completed " .. pending[1].title`,
	Comment: func(description string) string { return "-- " + description },
	Signature: func(item capability.Capability) string {
		object := func(schema capability.TypeSchema) string {
			return prompt.Fields(schema, luaTypes, func(name, fieldType string) string {
				return name + " = " + fieldType
			}, "{}", "{ ", " }")
		}
		return fmt.Sprintf("codemode.execute(%q, %s) -> %s",
			item.Name, object(item.Input), object(item.Output))
	},
}

func (runner *luaRunner) Language() string { return luaDialect.ID }

func (runner *luaRunner) Prompt(matches []capability.Match) string {
	return luaDialect.Render(matches)
}

func (runner *luaRunner) Name() string {
	return fmt.Sprintf("lua / Lua 5.1 (in-process, %s timeout, %d call budget)",
		runner.options.Timeout, runner.options.MaxCalls)
}

// Close is a no-op: the interpreter is created per run.
func (runner *luaRunner) Close() error { return nil }

func (runner *luaRunner) Run(ctx context.Context, mode codemode.CodeMode, source string) (any, error) {
	if mode == nil {
		return nil, errors.New("code mode runtime is required")
	}

	ctx, cancel := context.WithTimeout(ctx, runner.options.Timeout)
	defer cancel()

	// SkipOpenLibs leaves the interpreter empty; the loop below opens only the
	// pure-computation libraries, so io, os, package, and debug never exist.
	state := luavm.NewState(luavm.Options{SkipOpenLibs: true})
	defer state.Close()

	for _, library := range []struct {
		name string
		open luavm.LGFunction
	}{
		{luavm.BaseLibName, luavm.OpenBase},
		{luavm.TabLibName, luavm.OpenTable},
		{luavm.StringLibName, luavm.OpenString},
		{luavm.MathLibName, luavm.OpenMath},
	} {
		state.Push(state.NewFunction(library.open))
		state.Push(luavm.LString(library.name))
		if err := state.PCall(1, 0, nil); err != nil {
			return nil, fmt.Errorf("open lua library %s: %w", library.name, err)
		}
	}
	// Base brings a few escape hatches with it; remove the ones that let a
	// program reach outside its own evaluation.
	for _, global := range []string{"dofile", "loadfile", "load", "loadstring", "collectgarbage", "print"} {
		state.SetGlobal(global, luavm.LNil)
	}

	// SetContext wires cancellation into the interpreter's instruction loop, so
	// a runaway program is stopped at the timeout.
	state.SetContext(ctx)

	calls := 0
	execute := func(state *luavm.LState) int {
		name := state.CheckString(1)
		input := state.OptTable(2, state.NewTable())

		calls++
		if calls > runner.options.MaxCalls {
			state.RaiseError("call budget of %d exhausted", runner.options.MaxCalls)
			return 0
		}

		decoded, err := fromLua(input)
		if err != nil {
			state.RaiseError("encode input for %s: %s", name, err)
			return 0
		}
		request, err := json.Marshal(decoded)
		if err != nil {
			state.RaiseError("encode input for %s: %s", name, err)
			return 0
		}

		response, err := mode.Execute(ctx, name, request)
		if err != nil {
			state.RaiseError("%s", err)
			return 0
		}
		var output any
		if err := json.Unmarshal(response, &output); err != nil {
			state.RaiseError("decode output of %s: %s", name, err)
			return 0
		}

		state.Push(toLua(state, output))
		return 1
	}

	api := state.NewTable()
	state.SetField(api, "execute", state.NewFunction(execute))
	state.SetGlobal("codemode", api)

	if err := state.DoString(source); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("sandbox stopped: %w", ctx.Err())
		}
		return nil, fmt.Errorf("sandbox: %w", err)
	}

	if state.GetTop() == 0 {
		return nil, nil
	}
	return fromLua(state.Get(-1))
}

// toLua converts a JSON-decoded Go value into a Lua value. JSON arrays become
// tables indexed from 1, matching Lua's own convention.
func toLua(state *luavm.LState, value any) luavm.LValue {
	switch typed := value.(type) {
	case nil:
		return luavm.LNil
	case bool:
		return luavm.LBool(typed)
	case float64:
		return luavm.LNumber(typed)
	case string:
		return luavm.LString(typed)
	case []any:
		table := state.NewTable()
		for _, item := range typed {
			table.Append(toLua(state, item))
		}
		return table
	case map[string]any:
		table := state.NewTable()
		for key, item := range typed {
			state.SetField(table, key, toLua(state, item))
		}
		return table
	default:
		return luavm.LNil
	}
}

// fromLua converts a Lua value into a JSON-encodable Go value. A table with
// contiguous integer keys from 1 becomes a slice; anything else becomes a map,
// which is how Lua's single table type maps onto JSON's two.
func fromLua(value luavm.LValue) (any, error) {
	switch typed := value.(type) {
	case *luavm.LNilType, nil:
		return nil, nil
	case luavm.LBool:
		return bool(typed), nil
	case luavm.LNumber:
		return float64(typed), nil
	case luavm.LString:
		return string(typed), nil
	case *luavm.LTable:
		if length := typed.Len(); length > 0 {
			items := make([]any, 0, length)
			for i := 1; i <= length; i++ {
				item, err := fromLua(typed.RawGetInt(i))
				if err != nil {
					return nil, err
				}
				items = append(items, item)
			}
			return items, nil
		}
		result := map[string]any{}
		var conversionErr error
		typed.ForEach(func(key, item luavm.LValue) {
			if conversionErr != nil {
				return
			}
			name, ok := key.(luavm.LString)
			if !ok {
				conversionErr = fmt.Errorf("table key %s is not a string", key.String())
				return
			}
			converted, err := fromLua(item)
			if err != nil {
				conversionErr = err
				return
			}
			result[string(name)] = converted
		})
		return result, conversionErr
	default:
		return nil, fmt.Errorf("cannot represent lua %s in Go", value.Type())
	}
}
