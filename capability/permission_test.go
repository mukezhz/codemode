package capability_test

import (
	"strings"
	"testing"

	"github.com/mukezhz/codemode/capability"
)

func TestValidatePermission(t *testing.T) {
	for _, valid := range []string{"deals.write", "reports.read", "deals.discounts.write", "billing.refunds.manage", "crm.deals.delete", "v2.deals.read"} {
		if err := capability.ValidatePermission(valid); err != nil {
			t.Errorf("%q: %v", valid, err)
		}
	}
	for _, invalid := range []string{"", "write", "Deals.Write", "deals write", "deals.", ".deals", "deals..write",
		"deals.write ", "deals-write", "2deals.write", "deals.Write", "deals.discount", "reports.view", "deals.edit",
		"deals.read.all", strings.Repeat("a.", 70) + "read"} {
		if err := capability.ValidatePermission(invalid); err == nil {
			t.Errorf("%q passed", invalid)
		}
	}
}

func TestValidatePermissionsRejectsTyposAndUnknownNames(t *testing.T) {
	full := capability.Registry{Capabilities: []capability.Capability{
		{Name: "deals.move", Permissions: []string{"deals.write"}},
		{Name: "reports.pipeline", Permissions: []string{"reports.read"}},
	}}
	if err := full.ValidatePermissions([]string{"deals.write", "reports.read"}); err != nil {
		t.Fatal(err)
	}
	err := full.ValidatePermissions([]string{"deals.write", "deal.write", "Reports.Read"})
	if err == nil || !strings.Contains(err.Error(), `"deal.write" is not declared`) || !strings.Contains(err.Error(), `"Reports.Read" must look like`) {
		t.Fatalf("err = %v, want both problems named", err)
	}
}

func TestGrantsFollowsTheActionHierarchy(t *testing.T) {
	for _, test := range []struct {
		held, required string
		want           bool
	}{
		{"deals.read", "deals.read", true},
		{"deals.read", "deals.write", false},
		{"deals.write", "deals.read", true},
		{"deals.write", "deals.delete", false},
		{"deals.delete", "deals.delete", true},
		{"deals.delete", "deals.read", false},
		{"deals.manage", "deals.read", true},
		{"deals.manage", "deals.delete", true},
		{"deals.manage", "deals.discounts.write", true}, // and its sub-resources
		{"deals.write", "deals.discounts.write", false}, // write stays on its own resource
		{"deals.write", "deals.discounts.read", false},
		{"deals.discounts.manage", "deals.write", false}, // not upward
		{"deals.manage", "reports.read", false},          // not sideways
		{"deals.manage", "dealsx.read", false},           // a prefix of the name is not a parent
		{"crm.manage", "crm.deals.write", true},
	} {
		if got := capability.Grants(test.held, test.required); got != test.want {
			t.Errorf("Grants(%q, %q) = %v, want %v", test.held, test.required, got, test.want)
		}
	}
}

func TestForAppliesTheHierarchy(t *testing.T) {
	full := capability.Registry{Capabilities: []capability.Capability{
		{Name: "deals.list", Permissions: []string{"deals.read"}},
		{Name: "deals.move", Permissions: []string{"deals.write"}},
		{Name: "deals.remove", Permissions: []string{"deals.delete"}},
		{Name: "deals.discount", Permissions: []string{"deals.discounts.write"}},
		{Name: "reports.pipeline", Permissions: []string{"reports.read"}},
	}}
	for granted, want := range map[string]string{
		"deals.read":   "deals.list",
		"deals.write":  "deals.list,deals.move",
		"deals.delete": "deals.remove",
		"deals.manage": "deals.list,deals.move,deals.remove,deals.discount",
	} {
		if got := strings.Join(full.For(capability.Caller{Permissions: []string{granted}}).Names(), ","); got != want {
			t.Errorf("%s: %q, want %q", granted, got, want)
		}
	}
	if err := full.ValidatePermissions([]string{"deals.manage", "reports.manage"}); err != nil {
		t.Errorf("manage should be grantable for every resource: %v", err)
	}
}

// TestDynamicRoleTable is the table in docs/content/tenants.md, "Dynamic roles".
func TestDynamicRoleTable(t *testing.T) {
	full := capability.Registry{Capabilities: []capability.Capability{
		{Name: "deals.list", Permissions: []string{"deals.read"}},
		{Name: "deals.move", Permissions: []string{"deals.write"}},
		{Name: "deals.discount", Roles: []string{"admin"}, Permissions: []string{"deals.discounts.write"}},
		{Name: "reports.pipeline", Permissions: []string{"reports.read"}},
	}}
	for _, row := range []struct {
		role   string
		caller capability.Caller
		want   string
	}{
		{"Junior AE", capability.Caller{Permissions: []string{"deals.read"}}, "deals.list"},
		{"Senior AE", capability.Caller{Permissions: []string{"deals.read", "deals.write", "deals.discounts.write"}}, "deals.list,deals.move,deals.discount"},
		{"Sales Ops", capability.Caller{Permissions: []string{"deals.read", "reports.read"}}, "deals.list,reports.pipeline"},
		{"Revoked Intern", capability.Caller{}, ""},
		{"fixed role admin", capability.Caller{Roles: []string{"admin"}}, "deals.discount"},
		{"Deal Editor", capability.Caller{Permissions: []string{"deals.write"}}, "deals.list,deals.move"},
		{"Deals Admin", capability.Caller{Permissions: []string{"deals.manage"}}, "deals.list,deals.move,deals.discount"},
	} {
		if got := strings.Join(full.For(row.caller).Names(), ","); got != row.want {
			t.Errorf("%s: %q, want %q", row.role, got, row.want)
		}
	}
}
