# Observability

Every run gets an ID. Its events, its error and any plan it held carry that ID,
so one value finds everything that happened.

```go
result, err := agent.Run(ctx, mode, model, request, agent.Options{
	Runner:  runner,
	OnEvent: agent.LogEvents(logger), // log/slog
})
result.RunID // "7c1e40a2b9d3f051"
```

## Events

`Options.OnEvent` receives one `agent.Event` per step of a run, and
`PlanOptions.OnEvent` one per step of a plan:

| Kind | When | Useful fields |
| --- | --- | --- |
| `run.started` | the run begins | |
| `search.done` | capabilities matched | `Count`, `Duration` |
| `program.written` | the model wrote a program | `Attempt`, `Duration` |
| `program.failed` | it failed and goes back for repair | `Attempt`, `Err` |
| `call.done` | a capability ran | `Name`, `Duration`, `Err` |
| `call.held` | a write was held for approval | `Name` |
| `run.finished` | an answer, or a plan waiting | `PlanID`, `Duration` |
| `run.failed` | the run gave up | `Stage`, `Err` |
| `plan.held` | a plan was stored | `PlanID`, `Count` |
| `plan.applying` | approved; calls start | `PlanID` |
| `plan.applied` / `plan.failed` | every call ran / one failed | `Name`, `Err` |
| `plan.rejected` / `plan.expired` | decided against / never decided | `PlanID` |

A failing run reads like this with `LogEvents`:

```
level=INFO  msg=run.started     run=7c1e40a2b9d3f051
level=INFO  msg=search.done     run=7c1e40a2b9d3f051 count=5 took=1.2ms
level=INFO  msg=program.written run=7c1e40a2b9d3f051 attempt=1 took=2.1s
level=ERROR msg=program.failed  run=7c1e40a2b9d3f051 attempt=1 error="invalid input for tasks.list: status: want one of open, completed, got \"done\""
level=INFO  msg=program.written run=7c1e40a2b9d3f051 attempt=2 took=1.8s
level=INFO  msg=call.done       run=7c1e40a2b9d3f051 capability=tasks.list took=0.4ms
level=INFO  msg=run.finished    run=7c1e40a2b9d3f051 took=4.0s
```

`OnEvent` must not block. Send events to a channel or a buffered logger if the
sink is slow.

## Errors

Every error `agent.Run` returns — except `*agent.Pending`, which is not a
failure — is an `*agent.Error`:

```go
var failure *agent.Error
if errors.As(err, &failure) {
	failure.RunID // the same ID as the events
	failure.Stage // agent.StageCache, StageSearch, StageGenerate or StageRun
}
// run 7c1e40a2b9d3f051: run: gave up after 3 attempts: invalid input for tasks.list: status: ...
```

`errors.Is` sees through it: `ErrNotRetried`, `ErrTokenBudget`,
`codemode.ErrPermanent` and your own handler errors still match.

| Stage | Means | Look at |
| --- | --- | --- |
| `cache` | a cached program failed after writing | the program cache |
| `search` | search failed or matched nothing | capability descriptions, the caller's roles |
| `generate` | the model call failed | the provider, key, timeout |
| `run` | the program kept failing | the `program.failed` events: each says why |

## Tracing and metrics

The core links no telemetry SDK. Adapt `OnEvent` in your application:

```go
OnEvent: func(event agent.Event) {
	span := trace.SpanFromContext(ctx) // your OpenTelemetry span for the request
	span.AddEvent(event.Kind, trace.WithAttributes(
		attribute.String("codemode.run", event.RunID),
		attribute.String("codemode.plan", event.PlanID),
		attribute.String("codemode.capability", event.Name),
	))
	if event.Err != "" {
		span.RecordError(errors.New(event.Err))
	}
	runs.Add(ctx, 1, metric.WithAttributes(attribute.String("kind", event.Kind)))
},
```

Worth a dashboard: `plan.held` minus decided (plans waiting), the time from
`plan.held` to `plan.applying` (how long people take), `program.failed` per run
(how often the model needs repair), and `call.done` with `Err` by capability.
