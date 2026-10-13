# Permissions

A capability surface is not one permission question but three, and they have
different answers in different places. Conflating them is where the mistakes
are.

| Question | Answered by | Lives in |
| --- | --- | --- |
| Which capabilities **exist** for this caller? | `Registry.For` | the contract (`roles`, `permissions`), and what your app says the caller holds |
| Which **rows** may those capabilities touch? | your `ExecuteFunc` | your application |
| Does the model **know** what it cannot do? | the system prompt | your instructions |

Only the first is the library's. The second cannot be — the registry knows the
capability being called and nothing whatsoever about the data behind it. The
third looks optional and is not.

## Roles and permissions

Codemode stores no roles. Each request, your app says what the signed-in user
holds and codemode keeps the capabilities that allows. The contract can ask for
two kinds of thing:

| Your app has | Annotate the RPC with | Pass each request |
| --- | --- | --- |
| **Fixed roles** in code: `admin`, `manager`, `member` | `(codemode.roles) = "admin"` | `Roles`: the user's roles from the session |
| **Dynamic roles**: admins create "Senior AE" and tick boxes | `(codemode.permissions) = "deals.write"` | `Permissions`: what the user's roles grant, fetched from your app |
| Both | both options on the same RPC | both lists |

```protobuf
rpc Move(MoveDealRequest) returns (Deal) {
  option (codemode.description) = "Moves a deal to another stage.";
  option (codemode.permissions) = "deals.write";       // whoever an admin granted it
}
rpc Discount(DiscountRequest) returns (Deal) {
  option (codemode.description) = "Applies a discount to a deal.";
  option (codemode.roles) = "admin";                    // a fixed role
  option (codemode.permissions) = "deals.discounts.write"; // or a runtime grant
}
```

```go
permissions, err := app.PermissionsFor(ctx, session) // your database or API, every request
if err != nil {
	return err // fail closed
}
allowed := full.For(capability.Caller{Roles: session.Roles, Permissions: permissions})
mode, err := codemode.New(allowed, capability.Keyword(allowed), execute)
```

A caller may use an RPC when they hold **any** role it lists **or any**
permission it lists. A role is never matched against permissions, or the
reverse: a role an admin happens to call `deals.write` grants nothing.

### Naming and validation

Permissions follow one pattern, `resource.action`. The resource is one or more
lowercase segments separated by dots; the action is one of four: `read`,
`write`, `delete` or `manage`.

| Valid | Invalid |
| --- | --- |
| `deals.write`, `reports.read`, `deals.discounts.write`, `crm.deals.delete` | `write` (no resource), `Deals.Write` (uppercase), `deals.discount` and `reports.view` (not one of the four actions), `deals-write`, `deals..write`, `deals.write ` |

Something your app wants to control on its own, such as discounting or exporting,
is a **sub-resource** with one of the four actions, not a new action:
`deals.discounts.write`, `deals.exports.read`. That keeps every role screen a
plain grid of resources × read / write / delete / manage.

### Read, write, delete, manage

The four actions imply each other in one direction only:

| Granted | Satisfies | Meaning |
| --- | --- | --- |
| `deals.read` | itself | view only |
| `deals.write` | itself and `deals.read` | edit, which implies viewing |
| `deals.delete` | itself | deleting stays a separate, deliberate grant; `write` never implies it |
| `deals.manage` | every action on `deals` **and its sub-resources**, such as `deals.discounts.write` | everything about deals |

The proto still names exactly what each RPC needs: `List` asks for `deals.read`,
`Move` for `deals.write`. The hierarchy applies only when matching what a user
was granted, so annotations stay precise and admins can grant broadly. `write`
stays on its own resource (`deals.write` does not grant `deals.discounts.write`),
and `manage` reaches down, never up or sideways: `deals.discounts.manage` does not
grant `deals.write`, and `deals.manage` grants nothing on `reports`. An RPC that
should be open only to managers asks for `deals.manage` itself.

`capability.Grants(held, required)` is the rule, if you need it elsewhere in your
app (to show or hide UI, say), so the UI and the assistant agree.

They are checked in two places:

- **In the proto, at load.** `registry.FromFiles` rejects a malformed permission,
  naming the RPC, so a bad contract stops the app at startup instead of quietly
  denying everyone.
- **When an admin saves a role.** Check the submitted names against the protos
  before storing them:

  ```go
  if err := full.ValidatePermissions(submitted); err != nil {
  	return err // e.g. permission "deal.write" is not declared by any capability
  }
  ```

  It rejects names that break the pattern and names no capability accepts, and
  reports every problem at once. Build the role editor's checkboxes from
  `full.Permissions()`: every permission an RPC declares, plus `resource.manage`
  for each resource, so admins can only pick valid names.

At request time `For` does not fail on unknown names: your app's permission set
will include ones the assistant has no use for (`users.invite`), and those are
simply ignored. Validate when roles are *saved*, not when they are *used*.
`capability.ValidatePermission(name)` checks a single name against the pattern.

Because the registry is built per request, an admin's change to a role applies
on the user's next request; nothing is compiled in or cached by codemode. The
protos stay the ceiling: a runtime role can be given any permission the protos
name, and nothing they do not. `full.Permissions()` lists them all, which is what
a role-editing screen offers as checkboxes.
[Dynamic roles](/tenants#dynamic-roles-permissions-in-the-proto-grants-in-your-app)
covers limits with values ("discount up to 15%") and fetching permissions from an
app that is not written in Go.

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

A method's `(codemode.roles)` or `(codemode.permissions)` **replaces** both of its
service's defaults rather than adding to them, so one method of a restricted
service can be opened or locked down deliberately and visibly.

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

With permissions too, one rule decides: **a method that declares `roles` or
`permissions` uses only its own declaration; a method that declares neither
inherits both service defaults.**

| Service defaults | Method declares | Who may call the method |
| --- | --- | --- |
| `default_roles = "member"`, `default_permissions = "deals.read"` | nothing | members, or holders of `deals.read` |
| same | `roles = "admin"` | admin only: `deals.read` is **not** inherited |
| same | `permissions = "deals.write"` | holders of `deals.write` (or `deals.manage`) only |
| same | both | the method's roles or the method's permissions |

The second row is the one that matters. If defaults were replaced one at a time,
a method locked to `admin` would still be open to everyone holding the service's
default permission, and nobody reading the method would see it.

Rules that follow:

- **A method's declaration replaces the service defaults; it never adds to them.**
  To widen, repeat the default's roles and add the new one, as `Summary` does. To
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
allowed := full.For(capability.Caller{Roles: identity.Roles})
```

With the service above, a user with `["billing", "viewer"]` gets `billing.invoices`
and `billing.summary`, but not `billing.refund`. A user with no roles keeps the
unrestricted capabilities. [Roles and tenants](/tenants) builds the full request
path around this.

Roles that admins create at runtime cannot be named in a proto. Use
`(codemode.permissions)` / `(codemode.default_permissions)` for those RPCs
instead, with exactly the same rules as the table above:
[Roles and permissions](#roles-and-permissions).

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
allowed := full.For(capability.Caller{Roles: identity.Roles, Permissions: permissions})
mode, err := codemode.New(allowed, capability.Keyword(allowed), app.Execute)
```

Building a registry per request is cheap — a slice and a map. Passing the
*same* narrowed registry to both `codemode.New` and your search function is
what makes this one decision instead of two: the model is never told about a
capability it cannot call.

**Unknown names fail closed.** A role `"Admin"` — wrong case, a typo, a role
from a system that no longer exists — or a permission an admin deleted gets the
unrestricted capabilities and nothing more. A mistake in a role name loses
access, never gains it.

## Policy that is not in the contract

A plan a customer is on, a feature flag, a rule in a database, an experiment
tightening one capability to watch what happens:

```go
full = full.Require("admin", func(item capability.Capability) bool {
	return strings.HasSuffix(item.Name, ".delete")
})
```

`Require` adds `role` to each selected capability. One that required nothing
now requires it, which narrows it; one that already had roles or permissions
**also admits `role`**, which widens it for that role. To take a capability away
from callers, remove it instead, per request:

```go
allowed := full.For(caller)
if !slices.Contains(caller.Roles, "admin") {
	allowed = allowed.Select(func(item capability.Capability) bool {
		return !strings.HasSuffix(item.Name, ".delete")
	})
}
```

`Select` and `For` only ever remove capabilities.

## Assert what is public

`Unrestricted()` returns everything with neither roles nor permissions declared. That list is worth
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
