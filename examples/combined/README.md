# combined

One program, three capability sources: this application's own Go functions, a
protobuf service, and an MCP server's tools, joined with `codemode.Combine`.

| Capability | Source |
| --- | --- |
| `notes.add` | Go: `gosource.Func` in `main.go` |
| `todos.create`, `todos.list`, `todos.complete`, `todos.delete` | protobuf: [`examples/todo/proto/todo.proto`](../todo/proto/todo.proto) |
| `calendar.events` | MCP: an in-process server standing in for another team's service |

From the repository root:

```sh
make demo-combined             # or: go run ./examples/combined — no key, a fixed program
```

```
answer
  Scheduled today: Write the docs at 14:00
```

The program lists todos from the proto service, today's events from the MCP
server, and saves a note through the Go function, all in one run. Set
`ANTHROPIC_API_KEY` or `OPENAI_API_KEY` to have a model write it instead.

What to look at:

- `Combine` routes each call to the source that owns its RPC. A name or RPC
  defined twice is an error.
- `mcpsource.Import(..., "member")`: imported tools require the role the host
  names and count as writes, whatever the server claims. A caller with no role
  never sees `calendar.events`.
- Every call is validated against its shape, whichever source it came from.
