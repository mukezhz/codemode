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

// A typo in a role must lose access, never gain it.
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
	if got := strings.Join(tightened.ForRole("finance").Names(), ","); got != "tasks.list,billing.refund" {
		t.Errorf("finance sees %q", got)
	}
	if got := strings.Join(tightened.ForRole("admin").Names(), ","); got != "tasks.list,tasks.delete,billing.refund" {
		t.Errorf("an admin sees %q", got)
	}

	if got := len(full.Unrestricted().Capabilities); got != 2 {
		t.Fatalf("Require mutated the source registry: %d unrestricted", got)
	}
}

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

func TestForMatchesAnyRoleOrPermission(t *testing.T) {
	full := capability.Registry{Capabilities: []capability.Capability{
		withRoles("tasks.list"),
		withRoles("tasks.assign", "admin"),
		{Name: "deals.move", Permissions: []string{"deals.write"}},
		{Name: "deals.refund", Roles: []string{"admin"}, Permissions: []string{"billing.refund"}},
	}}
	for _, test := range []struct {
		caller capability.Caller
		want   string
	}{
		{capability.Caller{}, "tasks.list"},
		{capability.Caller{Roles: []string{"admin"}}, "tasks.list,tasks.assign,deals.refund"},
		{capability.Caller{Permissions: []string{"deals.write"}}, "tasks.list,deals.move"},
		{capability.Caller{Permissions: []string{"billing.refund"}}, "tasks.list,deals.refund"},
		{capability.Caller{Roles: []string{"deals.write"}}, "tasks.list"}, // a role is not a permission
		{capability.Caller{Roles: []string{" ", "Admin"}, Permissions: []string{"deals.Write"}}, "tasks.list"},
	} {
		if got := strings.Join(full.For(test.caller).Names(), ","); got != test.want {
			t.Errorf("For(%+v) = %q, want %q", test.caller, got, test.want)
		}
	}
	if got := strings.Join(full.Unrestricted().Names(), ","); got != "tasks.list" {
		t.Errorf("Unrestricted() = %q: a permission-only capability is restricted", got)
	}
	if got := strings.Join(full.Permissions(), ","); got != "billing.refund,deals.write" {
		t.Errorf("Permissions() = %q", got)
	}
}
