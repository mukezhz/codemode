package capability_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/mukezhz/codemode/capability"
)

func registryOf(names ...string) capability.Registry {
	built := capability.Registry{}
	for _, name := range names {
		built.Capabilities = append(built.Capabilities, capability.Capability{
			Name: name, RPC: "svc/" + name, Description: name,
		})
	}
	return built
}

func TestMergeCombinesSubsystems(t *testing.T) {
	merged, err := capability.Merge(
		registryOf("contacts.list", "contacts.create"),
		registryOf("deals.list"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(merged.Names(), ","); got != "contacts.list,contacts.create,deals.list" {
		t.Fatalf("names = %q, want registry order preserved", got)
	}
}

// Last-one-wins would let one team silently take over another's capability
// name, and the failure would show up as a model calling the wrong code.
func TestMergeRejectsADuplicateName(t *testing.T) {
	_, err := capability.Merge(registryOf("contacts.list"), registryOf("contacts.list"))
	if err == nil {
		t.Fatal("Merge accepted a duplicate capability name")
	}
	if !strings.Contains(err.Error(), "contacts.list") {
		t.Fatalf("error = %v, want it to name the collision", err)
	}
}

func TestSelectNarrowsTheSurface(t *testing.T) {
	full := registryOf("tasks.list", "tasks.delete", "reports.summary")

	readOnly := full.Select(func(item capability.Capability) bool {
		return !strings.HasSuffix(item.Name, ".delete")
	})
	if got := strings.Join(readOnly.Names(), ","); got != "tasks.list,reports.summary" {
		t.Fatalf("names = %q", got)
	}
	// Select returns a new registry; narrowing per request must not mutate the
	// one the application built at startup.
	if len(full.Capabilities) != 3 {
		t.Fatal("Select mutated the source registry")
	}
}

func TestNamespaceAndNamePredicates(t *testing.T) {
	full := registryOf("tasks.list", "tasks.delete", "reports.summary", "users.me")

	if got := strings.Join(full.Select(capability.InNamespace("reports", "users")).Names(), ","); got != "reports.summary,users.me" {
		t.Fatalf("InNamespace = %q", got)
	}
	if got := strings.Join(full.Select(capability.Named("tasks.list")).Names(), ","); got != "tasks.list" {
		t.Fatalf("Named = %q", got)
	}
	if got := strings.Join(full.Namespaces(), ","); got != "reports,tasks,users" {
		t.Fatalf("Namespaces = %q, want them sorted and deduplicated", got)
	}
}

func withRoles(name string, roles ...string) capability.Capability {
	return capability.Capability{Name: name, RPC: "svc/" + name, Roles: roles}
}

// The policy travels with the contract, so narrowing a registry is one call and
// there is no list in the host to keep in step.
func TestForRoleNarrowsToWhatTheContractsAllow(t *testing.T) {
	full := capability.Registry{Capabilities: []capability.Capability{
		withRoles("tasks.list"),
		withRoles("tasks.assign", "admin"),
		withRoles("billing.refund", "admin", "finance"),
		withRoles("reports.export", "finance"),
	}}

	for role, want := range map[string]string{
		"member":  "tasks.list",
		"admin":   "tasks.list,tasks.assign,billing.refund",
		"finance": "tasks.list,billing.refund,reports.export",
	} {
		if got := strings.Join(full.ForRole(role).Names(), ","); got != want {
			t.Errorf("ForRole(%q) = %q, want %q", role, got, want)
		}
	}
}

// An unrestricted capability is available to everyone, including a role nobody
// declared — but a restricted one is available to nobody who is not named. A
// typo in a role must lose access, never gain it.
func TestForRoleFailsClosedForUnknownRoles(t *testing.T) {
	full := capability.Registry{Capabilities: []capability.Capability{
		withRoles("tasks.list"),
		withRoles("tasks.assign", "admin"),
	}}

	for _, role := range []string{"", "Admin", "admins", "root"} {
		if got := strings.Join(full.ForRole(role).Names(), ","); got != "tasks.list" {
			t.Errorf("ForRole(%q) = %q, want only the unrestricted capability", role, got)
		}
	}
}

// Unrestricted is what a test asserts against, so a privileged RPC that arrives
// without its roles option is caught rather than quietly exposed.
func TestUnrestrictedAndRoles(t *testing.T) {
	full := capability.Registry{Capabilities: []capability.Capability{
		withRoles("tasks.list"),
		withRoles("tasks.assign", "admin"),
		withRoles("billing.refund", "finance", "admin"),
	}}

	if got := strings.Join(full.Unrestricted().Names(), ","); got != "tasks.list" {
		t.Errorf("Unrestricted() = %q", got)
	}
	if got := strings.Join(full.Roles(), ","); got != "admin,finance" {
		t.Errorf("Roles() = %q, want them deduplicated and sorted", got)
	}
}

// Require is for policy that is not part of the contract: a plan, a flag, a
// rule in a database, or an experiment tightening one capability to watch what
// happens.
func TestRequireNarrowsAndNeverWidens(t *testing.T) {
	full := capability.Registry{Capabilities: []capability.Capability{
		withRoles("tasks.list"),
		withRoles("tasks.delete"),
		withRoles("billing.refund", "finance"),
	}}

	tightened := full.Require("admin", func(item capability.Capability) bool {
		return item.Name != "tasks.list"
	})

	if got := strings.Join(tightened.ForRole("member").Names(), ","); got != "tasks.list" {
		t.Errorf("a member sees %q, want only the untouched capability", got)
	}
	// An existing role is kept, not replaced: finance still reaches its own.
	if got := strings.Join(tightened.ForRole("finance").Names(), ","); got != "tasks.list,billing.refund" {
		t.Errorf("finance sees %q", got)
	}
	if got := strings.Join(tightened.ForRole("admin").Names(), ","); got != "tasks.list,tasks.delete,billing.refund" {
		t.Errorf("an admin sees %q", got)
	}

	// The source registry is untouched, or narrowing one request would narrow
	// every later one.
	if got := len(full.Unrestricted().Capabilities); got != 2 {
		t.Fatalf("Require mutated the source registry: %d unrestricted", got)
	}
}

// Deriving twice from one registry must not let the two share backing storage.
func TestRequireDoesNotAliasRoles(t *testing.T) {
	full := capability.Registry{Capabilities: []capability.Capability{withRoles("tasks.delete", "finance")}}
	first := full.Require("admin", func(capability.Capability) bool { return true })
	_ = full.Require("auditor", func(capability.Capability) bool { return true })

	if slices.Contains(first.Capabilities[0].Roles, "auditor") {
		t.Fatalf("the first registry gained the second's role: %v", first.Capabilities[0].Roles)
	}
	if len(full.Capabilities[0].Roles) != 1 {
		t.Fatalf("the source gained a role: %v", full.Capabilities[0].Roles)
	}
}
