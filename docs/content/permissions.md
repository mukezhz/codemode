# Permissions

A capability surface is not one permission question but three, and they have
different answers in different places. Conflating them is where the mistakes
are.

| Question | Answered by | Lives in |
| --- | --- | --- |
| Which capabilities **exist** for this caller? | `Registry.ForRole` | the contract, `(codemode.roles)` |
| Which **rows** may those capabilities touch? | your `ExecuteFunc` | your application |
| Does the model **know** what it cannot do? | the system prompt | your instructions |

Only the first is the library's. The second cannot be — the registry knows the
capability being called and nothing whatsoever about the data behind it. The
third looks optional and is not.

## Declare the policy in the contract

```protobuf
service TaskService {
  option (codemode.namespace) = "tasks";

  rpc List(ListTasksRequest) returns (TaskListResponse) {
    option (codemode.description) = "Lists your own tasks, newest first.";
  }

  rpc ListFor(ListForRequest) returns (TaskListResponse) {
    option (codemode.description) = "Lists another user's tasks.";
    option (codemode.roles) = "admin";
  }
}
```

Roles are a space-separated list, so this is not admin-or-nothing:

```protobuf
option (codemode.roles) = "admin finance";
```

`(codemode.default_roles)` on a service sets a default its methods inherit. Use
it on a service where everything is privileged, so a **new RPC arrives
restricted** rather than open:

```protobuf
service AdminService {
  option (codemode.namespace) = "admin";
  option (codemode.default_roles) = "admin";

  rpc Impersonate(ImpersonateRequest) returns (SessionResponse) {
    option (codemode.description) = "Starts a session as another user.";
    option (codemode.roles) = "owner";       // narrower still
  }
}
```

A method's `(codemode.roles)` **replaces** its service's default rather than
adding to it, so one method of a restricted service can be opened deliberately
and visibly.

::: tip Why not a list of names in Go
A list of privileged capability names in your host is fail-open. Add a
privileged RPC, forget the list, and it is exposed to everyone — silently.
Nothing fails, no test breaks, and the first sign is somebody calling it.

With the annotation, the RPC and its policy are edited in the same place at the
same time.
:::

## Several roles on one RPC or service

Every role option is a **space-separated list**. A caller holding **any one** of
the listed roles may call the RPC.

### On one method

```protobuf
rpc Assign(AssignRequest) returns (Task) {
  option (codemode.description) = "Moves a task to another person.";
  option (codemode.roles) = "manager admin";        // manager OR admin
}
```

### On a whole service

```protobuf
service BillingService {
  option (codemode.namespace) = "billing";
  option (codemode.default_roles) = "billing admin"; // every method: billing OR admin

  rpc Invoices(InvoicesRequest) returns (InvoiceList) {
    option (codemode.description) = "Lists invoices.";            // billing, admin
  }
  rpc Refund(RefundRequest) returns (RefundResult) {
    option (codemode.description) = "Refunds an invoice.";
    option (codemode.roles) = "admin";                             // admin only
  }
  rpc Summary(SummaryRequest) returns (BillingSummary) {
    option (codemode.description) = "Totals for this month.";
    option (codemode.roles) = "billing admin viewer";              // adds viewer
  }
}
```

### How a method's roles are decided

| Service `default_roles` | Method `roles` | Who may call the method |
| --- | --- | --- |
| not set | not set | everyone |
| not set | `"admin"` | admin |
| not set | `"manager admin"` | manager or admin |
| `"billing admin"` | not set | billing or admin |
| `"billing admin"` | `"admin"` | admin (narrowed) |
| `"billing admin"` | `"billing admin viewer"` | billing, admin or viewer (widened) |
| `"billing admin"` | `""` | billing or admin: an empty string counts as not set |
| either | `" "` (whitespace only) | **build error**: `roles option is blank; remove it to place no restriction` |

Three rules follow from the table:

- **A method's `roles` replaces the service default; it never adds to it.** To
  widen, repeat the default's roles and add the new one, as `Summary` does. To
  narrow, list fewer.
- **A method in a restricted service cannot be made public.** There is no value
  meaning "everyone". Put a public method in a service without
  `default_roles`. This is deliberate: in a service declared privileged,
  opening a method to everyone should take a visible move, not one string.
- **Order and spacing do not matter.** `"admin manager"` and `"manager  admin"`
  are the same set. Role names are case-sensitive: `Admin` is not `admin`.

### What a caller with several roles gets

Pass every role the caller holds, fetched from your app per request:

```go
allowed := full.ForRoles(identity.Roles...)
```

With the service above, a user with `["billing", "viewer"]` gets `billing.invoices`
and `billing.summary`, but not `billing.refund`. A user with no roles keeps the
unrestricted capabilities. [Roles and tenants](/tenants) builds the full request
path around this.

Roles that admins create at runtime cannot be named in a proto. Name the
*permission* each RPC needs instead, and map roles to permissions in your
database: [Dynamic roles](/tenants#dynamic-roles-name-permissions-in-the-proto-not-roles).

### Pin each role's set in a test

```go
func TestRolesSeeTheIntendedCapabilities(t *testing.T) {
	want := map[string][]string{
		"viewer":  {"billing.summary", "tasks.list"},
		"billing": {"billing.invoices", "billing.summary", "tasks.list"},
		"admin":   {"billing.invoices", "billing.refund", "billing.summary", "tasks.assign", "tasks.list"},
	}
	for role, names := range want {
		got := slices.Sorted(slices.Values(full.ForRole(role).Names()))
		if !slices.Equal(got, names) {
			t.Errorf("%s sees %v, want %v", role, got, names)
		}
	}
}
```

A role quietly gaining a capability fails here, in review, instead of in
production.

## Narrow per request

```go
allowed := full.ForRole(string(identity.Role))
mode, err := codemode.New(allowed, search.New(allowed), app.Execute)
```

Building a registry per request is cheap — a slice and a map. Passing the
*same* narrowed registry to both `codemode.New` and your search function is
what makes this one decision instead of two: the model is never told about a
capability it cannot call.

**Unknown roles fail closed.** `ForRole("Admin")` — wrong case, a typo, a role
from a system that no longer exists — returns the unrestricted capabilities and
nothing more. A mistake in a role name loses access, never gains it.

## Policy that is not in the contract

A plan a customer is on, a feature flag, a rule in a database, an experiment
tightening one capability to watch what happens:

```go
full = full.Require("admin", func(item capability.Capability) bool {
	return strings.HasSuffix(item.Name, ".delete")
})
```

`Require` **only ever narrows**. A capability that already required a role gains
this one as an additional acceptable role; one that required nothing now
requires this. There is no call that widens a registry, because a mistake that
widens is the one nobody notices.

## Assert what is public

`Unrestricted()` returns everything with no roles declared. That list is worth
pinning in a test, because it is where a forgotten annotation lands:

```go
func TestOnlyTheIntendedCapabilitiesArePublic(t *testing.T) {
	if got := len(full.Unrestricted().Capabilities); got != 19 {
		t.Fatalf("unrestricted = %d, want 19: %v", got, full.Unrestricted().Names())
	}
}
```

This is the check a list in the host could not give you.

## What the sandbox gets

Nothing but the bridge:

```js
Object.keys(codemode)          // ["execute"]
typeof codemode.capabilities   // "undefined"
```

One binding, with the capability name as an argument. A program cannot
enumerate the surface, so it cannot discover what it is not allowed to call.
Resolution happens on the **host** side, against the registry built for this
caller:

```
codemode.execute("tasks.listFor", { owner: "linus" })
→ capability "tasks.listFor" is not registered
```

Identical to naming a capability that never existed — which is what stops a
program probing for what exists. Catching the error changes nothing; there is no
data behind it.

## Rows are a separate job

Withholding `tasks.assign` does nothing to stop `tasks.list` returning a
colleague's rows. That is your `ExecuteFunc`'s work, from an identity on the
context:

```go
identity, err := auth.From(ctx)
if err != nil {
	return nil, err          // a missing identity is a wiring bug: stop
}
scope := store.Only(identity.User)
```

::: danger Never put "who is asking" in a request message
If `tasks.list` takes an `owner`, the program can set it. A program written by a
model is untrusted input, and the first thing to try is someone else's name.
:::

For users with several roles, tenants, and per-tenant plans, see
[Roles and tenants](/tenants). See [Many services, many users](/scaling-up) for the rules that follow from
that — scope as a parameter, a zero value that reaches nothing, "not found"
rather than "forbidden".

## Tell the model what it cannot do

This is the part that looks optional.

Withholding a capability is **silent**. The model does not know it is missing
one, so it improvises with what it has. A member asking *"show me linus's
tasks"* has no `tasks.listFor`, calls `tasks.list` — correctly scoped to their
own rows — filters for linus, finds nothing, and reports:

> Linus has no tasks.

Every layer held. The answer was false.

Two things fix it, and both are prompt-level:

**Say what the caller can see.**

```go
Instructions: view.Instructions + "\n\n" + identity.Instructions(),
```

```
You are answering for ada, a member. Every capability returns only ada's own
tasks — never anyone else's. If the request asks about someone else, say that it
is outside what you can see. An empty result means you cannot see their work,
not that they have none.
```

**Give it somewhere to refuse.** Telling a model its limits is half the job; it
also needs a way to say so, or it finds a worse one. Asked for a full list with
only a search capability, one model called search once per letter of the
alphabet and reassembled the list — thirty-six calls, and a correct-looking
answer to a question it was not allowed to ask.

```json
{"unavailable": "I can only see your own tasks, not Linus's."}
```

Validation should reject a result that both refuses and answers, so the model
cannot hedge. **Refusing has to be a correct answer — reachable and rewarded —
or the model will fill the silence.**

## Testing denial

The example takes a flag, so you can see what a member experiences without
editing a proto and regenerating:

```sh
go run ./examples/taskreport -serve :8080 -restrict tasks.list
```

| | member | admin |
| --- | --- | --- |
| capabilities offered | 18 | 23 |
| `GET /api/tasks` | `400 capability "tasks.list" is not registered` | `200` |
| "what are my tasks?" | refused | a full table |

The REST endpoint fails too, and correctly: it goes through the same runtime as
the programs, so there is one answer to what a caller may run rather than two.
