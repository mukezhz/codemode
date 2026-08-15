package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	lua "github.com/yuin/gopher-lua"

	"github.com/mukezhz/code-mode/capability"
	"github.com/mukezhz/code-mode/codemode"
)

// luaRunner evaluates programs in Lua 5.1. It is the smallest and fastest of
// the three engines. The standard library is opened selectively: base, table,
// string, and math, but not io, os, package, or debug, so the program has no
// file access, no clock, and no module loader.
//
// ponytail: like the others, this runs in-process and is not a defence against
// a determined attacker sharing the process.
type luaRunner struct {
	options Options
}

// NewLua returns a Runner backed by the in-process Lua interpreter.
func NewLua(options Options) (Runner, error) {
	options, err := options.checked()
	if err != nil {
		return nil, err
	}
	return &luaRunner{options: options}, nil
}

// luaDialect is the dialect the Lua engine evaluates.
var luaDialect = dialect{
	id:    "lua",
	prose: "Lua 5.1",
	rules: []string{
		"Reply with statements at the top level of a chunk: no function wrapper, no require.",
		"Call capabilities as codemode.execute(name, input). It is synchronous and returns the decoded response as a table.",
		"End with a return statement whose value answers the request. Prefer a short human-readable string.",
		"Tables from a capability are arrays indexed from 1; use ipairs to walk them and # for length.",
		"String concatenation uses ..; convert numbers with tostring() first.",
		"Only the base, table, string, and math libraries exist. There is no io, os, or require.",
	},
	example: `local todos = codemode.execute("todos.list", {}).todos
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
	comment: func(description string) string { return "-- " + description },
	signature: func(item capability.Capability) string {
		object := func(schema capability.TypeSchema) string {
			return fields(schema, func(name, fieldType string) string {
				return name + " = " + fieldType
			}, "{}", "{ ", " }")
		}
		return fmt.Sprintf("codemode.execute(%q, %s) -> %s",
			item.Name, object(item.Input), object(item.Output))
	},
}

func (runner *luaRunner) Language() string { return luaDialect.id }

func (runner *luaRunner) Prompt(matches []capability.Match) string {
	return luaDialect.prompt(matches)
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
	state := lua.NewState(lua.Options{SkipOpenLibs: true})
	defer state.Close()

	for _, library := range []struct {
		name string
		open lua.LGFunction
	}{
		{lua.BaseLibName, lua.OpenBase},
		{lua.TabLibName, lua.OpenTable},
		{lua.StringLibName, lua.OpenString},
		{lua.MathLibName, lua.OpenMath},
	} {
		state.Push(state.NewFunction(library.open))
		state.Push(lua.LString(library.name))
		if err := state.PCall(1, 0, nil); err != nil {
			return nil, fmt.Errorf("open lua library %s: %w", library.name, err)
		}
	}
	// Base brings a few escape hatches with it; remove the ones that let a
	// program reach outside its own evaluation.
	for _, global := range []string{"dofile", "loadfile", "load", "loadstring", "collectgarbage", "print"} {
		state.SetGlobal(global, lua.LNil)
	}

	// SetContext wires cancellation into the interpreter's instruction loop, so
	// a runaway program is stopped at the timeout.
	state.SetContext(ctx)

	calls := 0
	execute := func(state *lua.LState) int {
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
func toLua(state *lua.LState, value any) lua.LValue {
	switch typed := value.(type) {
	case nil:
		return lua.LNil
	case bool:
		return lua.LBool(typed)
	case float64:
		return lua.LNumber(typed)
	case string:
		return lua.LString(typed)
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
		return lua.LNil
	}
}

// fromLua converts a Lua value into a JSON-encodable Go value. A table with
// contiguous integer keys from 1 becomes a slice; anything else becomes a map,
// which is how Lua's single table type maps onto JSON's two.
func fromLua(value lua.LValue) (any, error) {
	switch typed := value.(type) {
	case *lua.LNilType, nil:
		return nil, nil
	case lua.LBool:
		return bool(typed), nil
	case lua.LNumber:
		return float64(typed), nil
	case lua.LString:
		return string(typed), nil
	case *lua.LTable:
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
		typed.ForEach(func(key, item lua.LValue) {
			if conversionErr != nil {
				return
			}
			name, ok := key.(lua.LString)
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
