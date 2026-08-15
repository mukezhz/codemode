# taskreport

A task tracker with a web UI, driven in code mode. Tasks live in SQLite,
twenty-three capabilities are generated from two protobuf files, and a model
answers questions by writing **one JavaScript program** that composes them —
returning a document the browser renders as charts and tables.

Everything the application needs is in this directory. The only thing it takes
from the repository root is `proto/codemode.proto`, which declares the two
annotations that mark a service as a capability surface.

---

## Run it

### 1. Start the server

From the **repository root**:

```sh
go run ./examples/taskreport -serve :8080
```

That is the whole first step. Go is the only requirement — the frontend is built
and committed, so there is no Node install and no build to wait for.

You should see:

```
seeded 12 tasks into tasks.db

signed in as
  ada (member)

capabilities
  19 across projects, reports, tasks, users  — 4 withheld from this role

sandbox
  goja / JavaScript (in-process, 30s timeout, 60 call budget)

serving
  http://localhost:8080
  the board works; /api/chat needs -api-key and -base-url
```

Open <http://localhost:8080>. The **Board** works immediately — create, complete,
and delete tasks. **Ask** needs a model, which is the next step.

### 2. Add a model

```sh
cp examples/taskreport/.env.example examples/taskreport/.env
```

Edit it and put in a key. It ships pointed at DeepSeek:

```sh
OPENAI_BASE_URL=https://api.deepseek.com/v1
OPENAI_API_KEY=sk-your-key-here
OPENAI_MODEL=deepseek-chat
```

Any endpoint speaking the OpenAI protocol works — the file has commented
settings for OpenAI, Ollama, and OpenRouter too. Restart:

```sh
go run ./examples/taskreport -serve :8080
```

The banner now names the model, and **Ask** is live:

```
model
  openai/deepseek-chat at https://api.deepseek.com/v1
```

::: warning The `/v1` matters
DeepSeek's base URL needs the `/v1` suffix. Without it every request 404s.
:::

### 3. Ask it something

Go to <http://localhost:8080/chat> and try:

> what is overdue, and which of it is high priority?

One model turn produces one program. It calls several capabilities, does the
filtering in the sandbox, and returns a document rendered as stat cards, a
chart, and a table. **Show the program** under each answer reveals exactly what
ran.

### 4. Watch the scoping

Use the dropdown top-right to switch between `ada`, `linus`, and `grace`.

| | Board shows | Capabilities |
| --- | --- | --- |
| ada (member) | ada's 4 tasks | 19, four withheld |
| linus (member) | linus's 3 tasks | 19, four withheld |
| grace (admin) | all 12 | 23 |

Same data, same code. The difference is a scope derived from the identity and a
registry narrowed by role — see
[Many services, many users](../../docs/content/scaling-up.md).

---

## Without the .env

Every setting is also a flag, and flags win:

```sh
go run ./examples/taskreport -serve :8080 \
  -base-url https://api.deepseek.com/v1 \
  -api-key  "$DEEPSEEK_API_KEY" \
  -model    deepseek-chat
```

Precedence is **flag → exported environment → `.env`**. `.env` is gitignored;
`.env.example` is committed as documentation. `TASKREPORT_ENV=/path/to/other.env`
points somewhere else, for keeping a DeepSeek setup and a local Ollama one side
by side.

## Without a browser

The same application answers one question and exits:

```sh
# No key needed. A fixed program stands in for the model, so search, the
# registry, the sandbox, and the answer are all still real.
go run ./examples/taskreport

go run ./examples/taskreport -request "which projects are behind?"

# See the scoping from the command line.
go run ./examples/taskreport -user grace -role admin -request "team workload"

# Print the exact system prompt the model receives.
go run ./examples/taskreport -v
```

## Changing the frontend

Only needed if you edit anything under `web/src`:

```sh
mise install                                    # node 22 + pnpm 11
cd examples/taskreport/web
pnpm install
pnpm build                                      # writes dist/, which Go embeds
```

For a hot-reload loop, run two processes instead:

```sh
go run ./examples/taskreport -serve :8080       # data and model
cd examples/taskreport/web && pnpm dev          # assets on :5173, proxying /api
```

Then use <http://localhost:5173>.

## Changing the contracts

After editing `proto/task.proto` or `proto/user.proto`, from the repository
root:

```sh
make generate
```

The application derives its registry from the generated descriptors at startup,
so there is nothing to embed or keep in step. `make generate` also writes
`registry.json` through the CLI, and a test asserts the two agree.

## Resetting

```sh
rm tasks.db     # re-seeds twelve tasks on the next start
```

`tasks.db` is written to whatever directory you run from — the repository root,
if you followed the steps above. `-db :memory:` keeps nothing.

## Seeing what happened

When an answer looks wrong, the useful question is *which step* went wrong.
`-log debug` shows all of them:

```sh
go run ./examples/taskreport -serve :8080 -log debug
```

```
step=search       query="status report by project" limit=12 closest=reports.statusCounts
step=model        provider=openai/deepseek-chat system_bytes=4210
step=model.done   took=4.1s bytes=612
step=sandbox      engine=javascript lines=6

  ┌─ program (javascript) ─────────────────────────────────────────────
  │   1  const { groups } = codemode.execute("reports.byProject", {});
  │   2  if (groups.length === 0) {
  │   3    return "no projects";
  │   4  }
  │   5  const lines = groups.map(g => `${g.name}: ${g.open} open, ${g.completed} completed`);
  │   6  return "Status by project: " + lines.join("; ");
  └────────────────────────────────────────────────────────────────────

step=execute      name=reports.byProject input={} took=204µs output=98
step=sandbox.done took=3ms calls=1
step=answer       blocks=3 attempts=1 took=4.3s
```

**The program prints at every log level**, as a numbered block rather than a log
field — it is the single most useful artifact when an answer looks wrong, and
slog would render it as one line with the newlines escaped.

What prints is what the *interpreter* receives, not what the model said: the
agent strips markdown fences in between, and when the two differ the second is
the one that explains the answer.

At `info` you also get search, the model call, and the totals. `debug` adds the
whole system prompt and one line per capability call. Failures log at any level
— a rejected capability is the security boundary doing its job, and a failing
one is usually why an answer looks wrong.

The browser shows the same thing per answer: **Show the program** under any
result reveals the source and which capabilities search offered.

The tracing lives in [`trace/`](trace) and wraps `codemode.CodeMode` and
`llm.Client` from outside. The library logs nothing — the seams are interfaces,
so observing them needs no change to the thing observed.

## Troubleshooting

| Symptom | Cause |
| --- | --- |
| `The frontend is not built.` | Run `pnpm build` in `web/`, or use the dev server |
| Ask says `no model is configured` | No `.env` and no `-api-key` |
| Every model call 404s | Base URL is missing `/v1` |
| `tasks.db` keeps reappearing | It is created on first run; `rm` it to reseed |
| Board is empty after switching user | Correct — you are seeing that person's tasks |
| `reports.byOwner is not registered` | You are a member; switch to grace |
| An answer is wrong or zero | Run with `-log debug`, or click **Show the program** |

---

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

## Charts from a prompt

A program that returns a sentence can only ever produce a sentence. These
programs return a small **document** instead, and the browser renders it:

```json
{"title": "This week", "blocks": [
  {"type": "stats", "items": [{"label": "Overdue", "value": 3, "tone": "bad"}]},
  {"type": "bar",   "series": [{"label": "platform", "value": 3}]},
  {"type": "line",  "series": [{"label": "2026-08-15", "value": 2}]},
  {"type": "table", "columns": ["Task", "Due"], "rows": [["Ship it", "2026-08-09"]]},
  {"type": "text",  "text": "Three tasks are past their due date."}
]}
```

Five block types, one React component each. The schema lives in
[`view/`](view) and is enforced server-side before anything reaches the browser
— a model that invents a block type or leaves a table row short is told so while
the error can still be fed back for repair, rather than the page rendering a
blank panel.

`view.Instructions` is appended to the engine's system prompt through
`agent.Options.Instructions`, so the rules about the language and the single
binding stay where they belong.

Nothing in the renderer knows what a task is. A prompt about invoices would
render through the same components.

## Why this is one model turn

The default request produces a program that makes five capability calls, filters
three lists, totals a week of completions, and returns a paragraph. As ordinary
tool use that is five round trips, with every intermediate result — every task
row, every group count — passing back through the model's context.

Here the model is asked **once**. Only the finished document comes back.

## Capabilities

Generated from [`proto/task.proto`](proto/task.proto) and
[`proto/user.proto`](proto/user.proto) — this application's own contracts,
living beside it — and merged at startup. The split is
deliberate: `tasks` and `projects` are primitives, and `reports` holds only the
aggregates a database genuinely does better than a program would.

| Namespace | Capabilities |
| --- | --- |
| `tasks` | `create` `get` `list` `update` `complete` `reopen` `delete` `addTag` `removeTag` `search` · admin: `listFor` `assign` |
| `projects` | `list` `rename` |
| `reports` | `statusCounts` `byProject` `byPriority` `overdue` `completedBetween` `throughput` · admin: `byOwner` |
| `users` | `me` · admin: `list` |

The admin ones are marked `option (codemode.roles) = "admin";` in the protos, so
the policy lives beside the RPC rather than in a list in `main.go` that could
drift. They are **withheld from a member's registry entirely** — absent from the
map `Execute` resolves against, not merely hidden from the model, and therefore
absent from the prompt too.

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
| `-serve` | — | serve the web app, e.g. `:8080` |
| `-log` | `info` | `debug` adds the system prompt and every capability call |
| `-user` | `ada` | who is asking (command line only; the UI has a dropdown) |
| `-role` | `member` | `member` or `admin` |
| `-seed` | on | seed a sample backlog when the database is empty |

One environment variable, read by this command and not by the library:
`TASKREPORT_ENV` moves the `.env` file.

Run with `-v` once. Seeing the exact prompt — the dialect's rules, then the
capability signatures search selected — explains more than this README does.

## How the pieces fit

```
proto/      this application's contracts: task.proto, user.proto
gen/        their generated Go, committed so `go get` needs no buf
main.go     flags and wiring. The only thing here that reads the environment.
env.go      loads .env into the environment, before the flags are parsed.
serve.go    the -serve flag: HTTP, graceful shutdown.
store/      the application's own SQLite database. Knows nothing about models.
app/        ExecuteFunc: RPC path -> store call. The trust boundary.
auth/       who is asking. Identity on the context, never in a request.
search/     ranks capabilities; decides what the model is told about.
trace/      logs every step. Wraps the interfaces from outside.
view/       the document schema the programs return and the browser renders.
server/     REST for the board, /api/chat for the prompt.
web/        Vite + React + TanStack Router. Built output is embedded.
.env        your key. Copy .env.example.
```

Everything the application needs is in this directory. The only thing it takes
from the repository root is `proto/codemode.proto`, which declares the two
annotations — and a real consumer gets that with
`codemode-registry -print-proto`.

Some things worth noticing in the code:

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

**The board goes through CodeMode too.** `/api/tasks` calls `mode.Execute`
rather than the store, so there is one implementation of what "complete a task"
means and one place that scopes it. A REST endpoint that bypassed the runtime
would be a second place for the scoping to be wrong.

**Identity never enters a request.** No proto field carries a user. It rides on
the `context.Context` and every branch derives a `store.Scope` from it — because
a field a program can set is a field it can set to someone else's name. See
[Many services, many users](../../docs/content/scaling-up.md).

## Not production

The sandbox runs in-process. It denies ambient capabilities and bounds time and
call count, which is right for code your own host asked a model to write. It is
not a defence against a determined attacker — see
[the isolation ladder](../../docs/content/sandbox.md#the-isolation-ladder).

`ExecuteFunc` here runs with the application's full authority. A real deployment
carries identity on the `ctx` and checks it in every branch: the registry gates
which *capability* may run, never which *user* is asking.
