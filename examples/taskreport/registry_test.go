package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
	coderegistry "github.com/mukezhz/codemode/registry"
	"github.com/mukezhz/codemode/sandbox"
	"github.com/mukezhz/codemode/sandbox/goja"

	"github.com/mukezhz/codemode/examples/taskreport/app"
	"github.com/mukezhz/codemode/examples/taskreport/auth"
	taskv1 "github.com/mukezhz/codemode/examples/taskreport/gen/task/v1"
	userv1 "github.com/mukezhz/codemode/examples/taskreport/gen/user/v1"
	"github.com/mukezhz/codemode/examples/taskreport/search"
	taskstore "github.com/mukezhz/codemode/examples/taskreport/store"
)

// This application derives its registry at startup, but `make generate` also
// writes registry.json from the same contracts. If the two disagreed, a project
// that reviews the JSON in a pull request would be reviewing something other
// than what runs.
func TestDerivedRegistryMatchesTheGeneratedFile(t *testing.T) {
	generated, err := capability.LoadFile("registry.json")
	if err != nil {
		t.Skipf("registry.json is not generated: %v", err)
	}

	tasks, err := coderegistry.FromFiles(taskv1.File_task_proto)
	if err != nil {
		t.Fatal(err)
	}
	users, err := coderegistry.FromFiles(userv1.File_user_proto)
	if err != nil {
		t.Fatal(err)
	}
	derived, err := capability.Merge(tasks, users)
	if err != nil {
		t.Fatal(err)
	}

	// Compared by name: the two paths visit files in different orders, and
	// order is not part of what a registry means.
	want := byName(t, generated)
	got := byName(t, derived)
	if len(want) != len(got) {
		t.Fatalf("counts differ: registry.json %d, derived %d", len(want), len(got))
	}
	for name, encoded := range want {
		if got[name] != encoded {
			t.Errorf("%s differs:\n  file: %s\n derived: %s", name, encoded, got[name])
		}
	}
}

func byName(t *testing.T, built capability.Registry) map[string]string {
	t.Helper()
	byName := make(map[string]string, len(built.Capabilities))
	for _, item := range built.Capabilities {
		encoded, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		byName[item.Name] = string(encoded)
	}
	return byName
}

// The whole surface this application publishes, in one assertion. A capability
// appearing or vanishing should be a deliberate edit to this list.
func TestPublishedCapabilities(t *testing.T) {
	tasks, _ := coderegistry.FromFiles(taskv1.File_task_proto)
	users, _ := coderegistry.FromFiles(userv1.File_user_proto)
	full, err := capability.Merge(tasks, users)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Capabilities) != 23 {
		t.Fatalf("capabilities = %d, want 23: %v", len(full.Capabilities), full.Names())
	}

	member := full.ForRole("member")
	if len(member.Capabilities) != 19 {
		t.Fatalf("a member sees %d, want 19: %v", len(member.Capabilities), member.Names())
	}
}

// The prompt is the only thing a model sees. A capability withheld from a role
// must not appear in it — not as a signature, not as a name in a description,
// not anywhere — because a model told that tasks.assign exists will try it, and
// a request that fails is a worse answer than one that never reaches for it.
//
// This is the assertion that matches the design: search is built over the
// narrowed registry, so withholding is one decision rather than two.
func TestWithheldCapabilitiesNeverReachThePrompt(t *testing.T) {
	tasks, _ := coderegistry.FromFiles(taskv1.File_task_proto)
	users, _ := coderegistry.FromFiles(userv1.File_user_proto)
	full, err := capability.Merge(tasks, users)
	if err != nil {
		t.Fatal(err)
	}

	runner, err := goja.New(sandbox.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()

	member := full.ForRole("member")
	withheld := full.Select(func(item capability.Capability) bool {
		return !item.AllowsRole("member")
	}).Names()
	if len(withheld) == 0 {
		t.Fatal("the fixture withholds nothing")
	}

	// Questions chosen to pull hardest towards the admin capabilities.
	for _, question := range []string{
		"who on the team has the most overdue work?",
		"list everyone in the workspace",
		"reassign linus's tasks to me",
		"show me tasks.assign and reports.byOwner",
	} {
		matches, err := search.New(member)(context.Background(), question, 12)
		if err != nil {
			t.Fatal(err)
		}
		prompt := runner.Prompt(matches)
		for _, name := range withheld {
			if strings.Contains(prompt, name) {
				t.Errorf("%q produced a member prompt mentioning %s", question, name)
			}
		}
	}

	// An administrator's prompt does carry them, or the narrowing would be
	// withholding from everyone.
	matches, _ := search.New(full)(context.Background(), "who has the most overdue work?", 12)
	adminPrompt := runner.Prompt(matches)
	if !strings.Contains(adminPrompt, "reports.byOwner") {
		t.Error("an admin prompt is missing reports.byOwner")
	}
}

// Out of scope and non-existent must be indistinguishable. A distinct
// "forbidden" would let a program discover which capabilities exist by naming
// them, one at a time, and reading the errors.
func TestWithheldAndInventedCapabilitiesFailAlike(t *testing.T) {
	tasks, _ := coderegistry.FromFiles(taskv1.File_task_proto)
	users, _ := coderegistry.FromFiles(userv1.File_user_proto)
	full, _ := capability.Merge(tasks, users)
	member := full.ForRole("member")

	store, err := taskstore.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	mode, err := codemode.New(member, search.New(member), app.New(store).Execute)
	if err != nil {
		t.Fatal(err)
	}
	ctx := auth.NewContext(context.Background(), auth.Identity{User: "ada", Role: auth.RoleMember})

	_, withheldErr := mode.Execute(ctx, "reports.byOwner", []byte(`{}`))
	_, inventedErr := mode.Execute(ctx, "reports.byUnicorn", []byte(`{}`))
	if withheldErr == nil || inventedErr == nil {
		t.Fatal("one of the two calls succeeded")
	}

	shape := func(err error, name string) string {
		return strings.ReplaceAll(err.Error(), name, "NAME")
	}
	if shape(withheldErr, "reports.byOwner") != shape(inventedErr, "reports.byUnicorn") {
		t.Fatalf("the errors differ in shape, which leaks existence:\n withheld: %v\n invented: %v",
			withheldErr, inventedErr)
	}
}

// The policy lives in the contracts, so the set of unrestricted capabilities is
// something to assert rather than assume. If a privileged RPC is added and its
// (codemode.roles) forgotten, it lands here and this test says so — which is
// the failure mode a list in the host could not catch.
func TestOnlyTheIntendedCapabilitiesAreUnrestricted(t *testing.T) {
	tasks, _ := coderegistry.FromFiles(taskv1.File_task_proto)
	users, _ := coderegistry.FromFiles(userv1.File_user_proto)
	full, err := capability.Merge(tasks, users)
	if err != nil {
		t.Fatal(err)
	}

	restricted := full.Select(func(item capability.Capability) bool { return len(item.Roles) > 0 })
	if got := strings.Join(restricted.Names(), " "); got != "tasks.listFor tasks.assign reports.byOwner users.list" {
		t.Fatalf("restricted capabilities = %q", got)
	}
	if got := len(full.Unrestricted().Capabilities); got != 19 {
		t.Fatalf("unrestricted = %d, want 19", got)
	}
	if got := strings.Join(full.Roles(), ","); got != "admin" {
		t.Fatalf("roles named by the contracts = %q, want admin", got)
	}
}

// An unknown role gets the unrestricted surface and nothing more, rather than
// everything or an error. A typo in a role name must fail closed.
func TestUnknownRoleGetsOnlyTheUnrestrictedSurface(t *testing.T) {
	tasks, _ := coderegistry.FromFiles(taskv1.File_task_proto)
	users, _ := coderegistry.FromFiles(userv1.File_user_proto)
	full, _ := capability.Merge(tasks, users)

	for _, role := range []string{"", "Admin", "administrator", "superuser"} {
		allowed := full.ForRole(role)
		if len(allowed.Capabilities) != 19 {
			t.Errorf("role %q got %d capabilities, want the unrestricted 19", role, len(allowed.Capabilities))
		}
	}
}
