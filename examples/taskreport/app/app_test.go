package app_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mukezhz/codemode/codemode"
	"github.com/mukezhz/codemode/sandbox"
	"github.com/mukezhz/codemode/sandbox/goja"

	"github.com/mukezhz/codemode/capability"
	taskv1 "github.com/mukezhz/codemode/examples/taskreport/gen/task/v1"
	userv1 "github.com/mukezhz/codemode/examples/taskreport/gen/user/v1"
	coderegistry "github.com/mukezhz/codemode/registry"

	"github.com/mukezhz/codemode/examples/taskreport/app"
	"github.com/mukezhz/codemode/examples/taskreport/auth"
	"github.com/mukezhz/codemode/examples/taskreport/search"
	"github.com/mukezhz/codemode/examples/taskreport/store"
)

func full(t *testing.T) capability.Registry {
	t.Helper()
	tasks, err := coderegistry.FromFiles(taskv1.File_task_proto)
	if err != nil {
		t.Fatal(err)
	}
	users, err := coderegistry.FromFiles(userv1.File_user_proto)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := capability.Merge(tasks, users)
	if err != nil {
		t.Fatal(err)
	}
	return merged
}

func newMode(t *testing.T, identity auth.Identity) (codemode.CodeMode, *store.Store, context.Context) {
	t.Helper()
	tasks, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	tasks.SetClock(func() time.Time { return time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC) })
	t.Cleanup(func() { tasks.Close() })

	registry := full(t).ForRole(string(identity.Role))
	mode, err := codemode.New(registry, search.New(registry), app.New(tasks, "grace").Execute)
	if err != nil {
		t.Fatal(err)
	}
	return mode, tasks, auth.NewContext(context.Background(), identity)
}

func member(t *testing.T, name string) (codemode.CodeMode, *store.Store, context.Context) {
	t.Helper()
	return newMode(t, auth.Identity{User: name, Role: auth.RoleMember})
}

func run(t *testing.T, ctx context.Context, mode codemode.CodeMode, program string) any {
	t.Helper()
	runner, err := goja.New(sandbox.Options{Timeout: 5 * time.Second, MaxCalls: 60})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()

	value, err := runner.Run(ctx, mode, program)
	if err != nil {
		t.Fatalf("program failed: %v\n%s", err, program)
	}
	return value
}

// Every published capability must have a dispatch case.
func TestEveryPublishedCapabilityIsImplemented(t *testing.T) {
	mode, _, ctx := newMode(t, auth.Identity{User: "grace", Role: auth.RoleAdmin})

	for _, item := range full(t).Capabilities {
		t.Run(item.Name, func(t *testing.T) {
			_, err := mode.Execute(ctx, item.Name, json.RawMessage(`{}`))
			if err != nil && strings.Contains(err.Error(), "is not implemented") {
				t.Fatalf("%s is in the registry but has no implementation", item.Name)
			}
		})
	}
}

func TestProgramComposesTheWholeWorkflow(t *testing.T) {
	mode, tasks, ctx := member(t, "ada")

	value := run(t, ctx, mode, `
		codemode.execute("tasks.create", { title: "Write docs", project: "docs", priority: "high", due_date: "2026-08-10" });
		codemode.execute("tasks.create", { title: "Fix build",  project: "platform", priority: "low" });
		const third = codemode.execute("tasks.create", { title: "Ship it", project: "platform", priority: "high" }).task;

		codemode.execute("tasks.complete", { id: third.id });
		codemode.execute("tasks.addTag", { id: third.id, tag: "release" });

		const counts = codemode.execute("reports.statusCounts", {});
		const overdue = codemode.execute("reports.overdue", {}).tasks;
		const byProject = codemode.execute("reports.byProject", {}).groups;

		return counts.open + " open, " + counts.completed + " done, " +
		       overdue.length + " overdue, " + byProject.length + " projects";
	`)

	if value != "2 open, 1 done, 1 overdue, 2 projects" {
		t.Fatalf("answer = %#v", value)
	}
	stored, err := tasks.List(store.Everyone(), store.Filter{Status: "completed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || len(stored[0].Tags) != 1 || stored[0].Tags[0] != "release" {
		t.Fatalf("stored = %+v, want the completed task tagged release", stored)
	}
}

// Search does not authorize; a name absent from the registry is rejected before app code runs.
func TestInventedCapabilityIsRejected(t *testing.T) {
	mode, _, ctx := member(t, "ada")
	runner, err := goja.New(sandbox.Options{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()

	_, err = runner.Run(ctx, mode,
		`return codemode.execute("tasks.dropDatabase", {});`)
	if err == nil {
		t.Fatal("the sandbox executed a capability that does not exist")
	}
	if !strings.Contains(err.Error(), "tasks.dropDatabase") {
		t.Fatalf("error = %v, want it to name the rejected capability", err)
	}
}

func TestUnknownRequestFieldIsRejected(t *testing.T) {
	mode, _, ctx := member(t, "ada")
	_, err := mode.Execute(ctx, "tasks.create",
		json.RawMessage(`{"title":"x","assignee":"nobody"}`))
	if err == nil {
		t.Fatal("Execute accepted an unknown field")
	}
	if !strings.Contains(err.Error(), "assignee") {
		t.Fatalf("error = %v, want it to name the unknown field", err)
	}
}

// Application errors must reach the program as failures, not empty results.
func TestApplicationErrorsSurfaceInTheProgram(t *testing.T) {
	mode, _, ctx := member(t, "ada")
	runner, err := goja.New(sandbox.Options{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()

	_, err = runner.Run(ctx, mode,
		`return codemode.execute("tasks.complete", { id: 9999 });`)
	if err == nil {
		t.Fatal("completing a missing task succeeded")
	}
	if !strings.Contains(err.Error(), "9999") {
		t.Fatalf("error = %v, want it to name the missing task", err)
	}
}

func TestCallBudgetStopsARunawayProgram(t *testing.T) {
	mode, _, ctx := member(t, "ada")
	runner, err := goja.New(sandbox.Options{Timeout: 5 * time.Second, MaxCalls: 5})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()

	_, err = runner.Run(ctx, mode, `
		for (let index = 0; index < 100; index++) {
			codemode.execute("tasks.create", { title: "spam " + index });
		}
		return "unreachable";
	`)
	if err == nil {
		t.Fatal("the call budget did not stop the program")
	}
}

func TestOneMemberCannotSeeAnother(t *testing.T) {
	mode, tasks, ctx := member(t, "ada")
	if _, err := tasks.Create("linus", "Linus's secret", "docs", "high", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Create("ada", "Ada's task", "platform", "low", "", nil); err != nil {
		t.Fatal(err)
	}

	value := run(t, ctx, mode, `
		const mine = codemode.execute("tasks.list", {}).tasks;
		return mine.map(task => task.title).join(",");
	`)
	if value != "Ada's task" {
		t.Fatalf("tasks.list returned %#v, want only Ada's own", value)
	}

	// Reports must be scoped too, or totals leak.
	counts := run(t, ctx, mode, `return codemode.execute("reports.statusCounts", {}).total;`)
	if counts != int64(1) {
		t.Fatalf("statusCounts total = %#v, want 1", counts)
	}
}

// Another user's task ID reports "not found", not "forbidden".
func TestKnowingAnIDIsNotAccess(t *testing.T) {
	mode, tasks, ctx := member(t, "ada")
	theirs, err := tasks.Create("linus", "Linus's task", "docs", "high", "", nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, program := range []string{
		`return codemode.execute("tasks.get", { id: ID });`,
		`return codemode.execute("tasks.complete", { id: ID });`,
		`return codemode.execute("tasks.delete", { id: ID });`,
		`return codemode.execute("tasks.update", { id: ID, title: "mine now" });`,
	} {
		source := strings.ReplaceAll(program, "ID", strconv.FormatInt(theirs.ID, 10))
		runner, err := goja.New(sandbox.Options{Timeout: 5 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		_, err = runner.Run(ctx, mode, source)
		runner.Close()
		if err == nil {
			t.Fatalf("a member reached another user's task: %s", source)
		}
		if strings.Contains(strings.ToLower(err.Error()), "forbidden") {
			t.Fatalf("error distinguishes forbidden from missing, which leaks existence: %v", err)
		}
	}

	still, err := tasks.Get(store.Everyone(), theirs.ID)
	if err != nil || still.Title != "Linus's task" || still.Completed {
		t.Fatalf("task = %+v, err = %v; want it unchanged", still, err)
	}
}

func TestAdminCapabilitiesAreAbsentForMembers(t *testing.T) {
	mode, _, ctx := member(t, "ada")

	for _, name := range []string{"users.list", "tasks.listFor", "tasks.assign", "reports.byOwner"} {
		_, err := mode.Execute(ctx, name, json.RawMessage(`{}`))
		if err == nil {
			t.Fatalf("%s ran for a member", name)
		}
		if !strings.Contains(err.Error(), "is not registered") {
			t.Fatalf("%s failed with %v, want the registry to reject it", name, err)
		}
	}
}

// Admin checks still fail if a member is wrongly handed the full registry.
func TestAdminCheckHoldsEvenWithAWideRegistry(t *testing.T) {
	tasks, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer tasks.Close()

	registry := full(t)
	mode, err := codemode.New(registry, search.New(registry), app.New(tasks, "grace").Execute)
	if err != nil {
		t.Fatal(err)
	}
	ctx := auth.NewContext(context.Background(), auth.Identity{User: "ada", Role: auth.RoleMember})

	_, err = mode.Execute(ctx, "tasks.assign", json.RawMessage(`{"id":1,"owner":"ada"}`))
	if err == nil {
		t.Fatal("tasks.assign ran for a member given the full registry")
	}
	if !strings.Contains(err.Error(), "admin") {
		t.Fatalf("error = %v, want the role check to reject it", err)
	}
}

func TestAdminSeesEveryoneAndMeReflectsTheContext(t *testing.T) {
	mode, tasks, ctx := newMode(t, auth.Identity{User: "grace", Role: auth.RoleAdmin})
	tasks.Create("ada", "Ada's task", "platform", "low", "", nil)
	tasks.Create("linus", "Linus's task", "docs", "high", "", nil)

	value := run(t, ctx, mode, `
		const me = codemode.execute("users.me", {}).user;
		const byOwner = codemode.execute("reports.byOwner", {}).groups;
		const linus = codemode.execute("tasks.listFor", { owner: "linus" }).tasks;
		return me.name + "/" + me.role + " sees " + byOwner.length + " owners, linus has " + linus.length;
	`)
	if value != "grace/admin sees 2 owners, linus has 1" {
		t.Fatalf("answer = %#v", value)
	}
}

func TestNoIdentityIsRejected(t *testing.T) {
	mode, _, _ := member(t, "ada")
	if _, err := mode.Execute(context.Background(), "tasks.list", json.RawMessage(`{}`)); err == nil {
		t.Fatal("Execute ran without an identity on the context")
	}
}

func TestCreatedTasksBelongToTheCaller(t *testing.T) {
	mode, tasks, ctx := member(t, "ada")

	run(t, ctx, mode, `codemode.execute("tasks.create", { title: "Mine" }); return "done";`)

	created, err := tasks.List(store.Everyone(), store.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 1 || created[0].Owner != "ada" {
		t.Fatalf("tasks = %+v, want one owned by ada", created)
	}

	_, err = mode.Execute(ctx, "tasks.create", json.RawMessage(`{"title":"x","owner":"linus"}`))
	if err == nil {
		t.Fatal("tasks.create accepted an owner field")
	}
}

// The prompt must say what the caller can see, so an empty result is not read as absence.
func TestIdentityInstructionsDescribeTheCallersReach(t *testing.T) {
	member := auth.Identity{User: "ada", Role: auth.RoleMember}.Instructions()

	for _, want := range []string{"ada", "member", "own tasks", "outside what you can see"} {
		if !strings.Contains(member, want) {
			t.Errorf("a member's instructions omit %q:\n%s", want, member)
		}
	}
	if !strings.Contains(member, "empty result means you cannot see their work") {
		t.Errorf("a member's instructions do not warn against reading an empty result as absence:\n%s", member)
	}

	admin := auth.Identity{User: "grace", Role: auth.RoleAdmin}.Instructions()
	if !strings.Contains(admin, "whole workspace") {
		t.Errorf("an administrator's instructions do not say their reach is the workspace:\n%s", admin)
	}
	if strings.Contains(admin, "outside what you can see") {
		t.Errorf("an administrator was told they cannot see the workspace:\n%s", admin)
	}
}

// The sandbox gets one binding, no capability list.
func TestSandboxHoldsOnlyTheBridge(t *testing.T) {
	mode, _, ctx := member(t, "ada")

	if got := run(t, ctx, mode, `return Object.keys(codemode).join(",");`); got != "execute" {
		t.Fatalf("the sandbox global holds %q, want only execute", got)
	}
	for _, probe := range []string{"list", "capabilities", "search", "registry"} {
		got := run(t, ctx, mode, `return typeof codemode.`+probe+`;`)
		if got != "undefined" {
			t.Errorf("codemode.%s is %v; the sandbox can enumerate the surface", probe, got)
		}
	}
}

// Withheld capabilities fail from the sandbox however the program asks.
func TestSandboxCannotReachWithheldCapabilities(t *testing.T) {
	mode, tasks, ctx := member(t, "ada")
	if _, err := tasks.Create("linus", "Linus's task", "docs", "high", "", nil); err != nil {
		t.Fatal(err)
	}

	runner, err := goja.New(sandbox.Options{Timeout: 5 * time.Second, MaxCalls: 20})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()

	for _, program := range []string{
		`return codemode.execute("tasks.listFor", { owner: "linus" });`,
		`return codemode.execute("reports.byOwner", {});`,
		`return codemode.execute("users.list", {});`,
		`return codemode.execute("tasks.assign", { id: 1, owner: "ada" });`,
	} {
		if _, err := runner.Run(ctx, mode, program); err == nil {
			t.Fatalf("the sandbox executed a withheld capability: %s", program)
		}
	}

	value := run(t, ctx, mode, `
		try {
			return "leaked: " + JSON.stringify(codemode.execute("tasks.listFor", { owner: "linus" }));
		} catch (failure) {
			return "refused";
		}
	`)
	if got, ok := value.(string); !ok || !strings.HasPrefix(got, "refused") {
		t.Fatalf("a caught rejection yielded %#v", value)
	}
}
