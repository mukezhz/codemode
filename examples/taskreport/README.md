# taskreport

A task tracker with reporting, driven in code mode. Tasks live in SQLite,
eighteen capabilities are generated from protobuf contracts, and a model answers
questions by writing **one JavaScript program** that composes them.

```sh
# No key needed: search still ranks, the registry still authorizes, and a fixed
# program stands in for the model so the mechanism is inspectable.
go run ./examples/taskreport

# With DeepSeek, or any OpenAI-compatible endpoint.
go run ./examples/taskreport \
  -base-url https://api.deepseek.com/v1 \
  -api-key  "$DEEPSEEK_API_KEY" \
  -model    deepseek-chat \
  -request  "which projects are behind, and what should I do first?"
```

The first run seeds twelve sample tasks dated relative to today, so the overdue
and throughput reports have something to say.

## Things to ask it

```
"give me a status report across every project"
"what is overdue, and which of it is high priority?"
"how much did we finish this week compared to what is still open?"
"move everything in the docs project to documentation, then show me the split"
"create three tasks for the launch checklist and tag them release"
"what should I work on next?"
```

The last one is the interesting one. Nothing in the registry answers "what
should I work on next" — the model has to call several capabilities, filter and
rank the results in the sandbox, and decide. That is the whole argument for code
mode: the composition is the program, not a chain of round trips.

## Why this is one model turn

The default request produces a program that makes five capability calls, filters
three lists, totals a week of completions, and returns a paragraph. As ordinary
tool use that is five round trips, with every intermediate result — every task
row, every group count — passing back through the model's context.

Here the model is asked **once**. Only the final string comes back.

## Capabilities

Generated from [`proto/task.proto`](../../proto/task.proto). The split is
deliberate: `tasks` and `projects` are primitives, and `reports` holds only the
aggregates a database genuinely does better than a program would.

| Namespace | Capabilities |
| --- | --- |
| `tasks` | `create` `get` `list` `update` `complete` `reopen` `delete` `addTag` `removeTag` `search` |
| `projects` | `list` `rename` |
| `reports` | `statusCounts` `byProject` `byPriority` `overdue` `completedBetween` `throughput` |

Anything needing judgement — what counts as behind, how to phrase a summary,
what to do first — stays in the program the model writes. There is no
`reports.whatShouldIDoNext` RPC, and there should not be.

## Flags

| Flag | Default | |
| --- | --- | --- |
| `-request` | a status report | what to ask |
| `-base-url` | `$OPENAI_BASE_URL` | OpenAI-compatible endpoint |
| `-api-key` | `$OPENAI_API_KEY` | key for that endpoint |
| `-model` | `deepseek-chat` | model name |
| `-temperature` | unset | negative leaves the provider's default |
| `-limit` | 12 | how many capabilities are described to the model |
| `-attempts` | 3 | programs generated before giving up |
| `-instructions` | — | extra guidance appended to the system prompt |
| `-db` | `tasks.db` | `:memory:` keeps nothing |
| `-v` | off | also print the system prompt |

Run with `-v` once. Seeing the exact prompt — the dialect's rules, then the
capability signatures search selected — explains more than this README does.

## How the pieces fit

```
main.go            flags and wiring. The only thing here that reads the environment.
store/             the application's own SQLite database. Knows nothing about models.
app/               ExecuteFunc: RPC path -> store call. The trust boundary.
search/            ranks capabilities by word overlap; decides what the model is told about.
```

Four things are worth noticing in the code:

**The library reads no environment.** This command reads flags and `$OPENAI_*`,
then hands the library an explicit `llm.Options`. Swap the flags for your config
file and nothing in the library changes.

**The registry is derived, not generated.** `registry.FromFiles(taskv1.File_task_proto)`
reads the descriptors already compiled into the binary. No `registry.json`, no
`go:embed`, no build step, and it cannot drift from the contracts.

**Search and the allowlist are separate lists.** Search decides what the model
*knows about*; the registry decides what may *run*. `app_test.go` demonstrates
it: a program calling `tasks.dropDatabase` fails a map lookup before any
application code executes.

**Strict decoding.** `app` rejects unknown request fields, so a model that
invents `assignee` is told on the first call — while the error can still be fed
back for repair — rather than having the value dropped and the answer come out
quietly wrong.

## Not production

The sandbox runs in-process. It denies ambient capabilities and bounds time and
call count, which is right for code your own host asked a model to write. It is
not a defence against a determined attacker — see
[the isolation ladder](../../docs/content/sandbox.md#the-isolation-ladder).

`ExecuteFunc` here runs with the application's full authority. A real deployment
carries identity on the `ctx` and checks it in every branch: the registry gates
which *capability* may run, never which *user* is asking.
