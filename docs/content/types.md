# Types and validation

Codemode checks values at the two places the model's program meets your code.

| Where | What is checked | On a mismatch |
| --- | --- | --- |
| Each `codemode.execute(name, input)` | `input` against the capability's input shape | the call fails before your handler runs; the error names the field, and the model repairs the program |
| The program's return value, with `agent.RunAs[T]` | the value against `T` | the model is told what was wrong and repairs the program |

A program that already ran a write is never repaired, so a write cannot happen
twice. See [Writes and retries](/writes).

## Input

Every source declares an input shape, and every call is validated against it:

- an unknown field is rejected, naming the fields that exist;
- each value must have its declared kind: `string`, `bool`, `number`,
  `integer` (no fraction), lists, maps, nested shapes and enums;
- a field the shape marks required must be present and not `null`;
- at most one field of a `oneof` group may be set.

Errors carry the path, for example `items[2].id: want integer, got "2"`.
Nothing that fails validation is recorded as a call, so it never counts as a
write.

What counts as required depends on the source:

| Source | Required | Optional |
| --- | --- | --- |
| `gosource` | every field without `omitempty`, `omitzero` or a pointer | the rest |
| `mcpsource` | the JSON Schema's `required` | the rest |
| protobuf | nothing: proto3 fields default to zero | `optional` fields are shown as optional |

## Go structs

`gosource` and `RunAs` read Go types the way `encoding/json` writes them:

| Go | Shape |
| --- | --- |
| `string`, `bool` | `string`, `bool` |
| `int`, `int64`, `uint32`, ... | `integer` |
| `float64`, `float32` | `number` |
| `[]T`, `[N]T` | list of `T` |
| `map[string]T`, `map[int]T` | map |
| `[]byte` | base64 string |
| `time.Time` | RFC 3339 string |
| `json.RawMessage`, `any` | anything |
| a struct | a named shape, declared once |
| a string type with `Enum() []string` | one of those values |
| `,string` tag option | string |

Embedded structs are flattened. A `desc:"..."` tag describes a field to the
model. A type whose JSON shape reflection cannot know — a custom
`json.Marshaler`, a channel, a function — is an error when the source is built,
never a silent `any`.

A nil slice or map in a response is sent as `[]` or `{}`, because the prompt
promised the model a list.

## Typed answers

```go
type Revenue struct {
	Total       float64 `json:"total"`
	TopCustomer string  `json:"top_customer"`
}

revenue, result, err := agent.RunAs[Revenue](ctx, mode, model, request, options)
```

The model is told the shape in a sentence every engine reads alike, the return
value is checked and decoded into `T`, and a mismatch is repaired like any other
failure, within `Options.Attempts`. `RunAs[string]` asks for a string. The shape
is part of the cache key, so a cached program is reused only for the same `T`.
