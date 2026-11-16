# shop

The [quickstart](../../docs/content/quickstart.md) as a runnable example: two
capabilities from typed Go functions, an answer that comes back as a Go struct,
and a refund that waits for you to approve it.

```
main.go       the shop: structs, two functions, gosource, RunAs, ApplyPlan
main_test.go  typed answer, approval, access and validation, with no model
```

From the repository root:

```sh
make demo-shop                 # or: go run ./examples/shop — no key, a fixed program
```

```
2 paid orders, 141.5 in total
```

With a model:

```sh
export ANTHROPIC_API_KEY=sk-ant-...       # or OPENAI_API_KEY (+ OPENAI_BASE_URL, OPENAI_MODEL)
go run ./examples/shop                    # revenue and top customer, as a Revenue struct
go run ./examples/shop "refund order 1"   # held; answer y to apply it
```

What to look at:

- `ListOrders`, `Orders`, `Status` are the schema. `Status` has `Enum()`, so
  `{status: "lost"}` is rejected before `Shop.List` runs.
- `orders.refund` is `Roles("support")` and `NeedsApproval()`: a customer
  cannot see it, and support must approve each one.
- `agent.RunAs[Revenue]` returns a validated `Revenue`; a program that returns a
  sentence is asked to return the object instead.
