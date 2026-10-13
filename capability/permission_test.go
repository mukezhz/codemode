package capability_test

import (
	"strings"
	"testing"

	"github.com/mukezhz/codemode/capability"
)

func TestValidatePermission(t *testing.T) {
	for _, valid := range []string{"deals.write", "reports.view", "billing.refund_partial", "crm.deals.write", "v2.deals.read"} {
		if err := capability.ValidatePermission(valid); err != nil {
			t.Errorf("%q: %v", valid, err)
		}
	}
	for _, invalid := range []string{"", "write", "Deals.Write", "deals write", "deals.", ".deals", "deals..write",
		"deals.write ", "deals-write", "2deals.write", "deals.Write", strings.Repeat("a.", 70) + "b"} {
		if err := capability.ValidatePermission(invalid); err == nil {
			t.Errorf("%q passed", invalid)
		}
	}
}

func TestValidatePermissionsRejectsTyposAndUnknownNames(t *testing.T) {
	full := capability.Registry{Capabilities: []capability.Capability{
		{Name: "deals.move", Permissions: []string{"deals.write"}},
		{Name: "reports.pipeline", Permissions: []string{"reports.view"}},
	}}
	if err := full.ValidatePermissions([]string{"deals.write", "reports.view"}); err != nil {
		t.Fatal(err)
	}
	err := full.ValidatePermissions([]string{"deals.write", "deal.write", "Reports.View"})
	if err == nil || !strings.Contains(err.Error(), `"deal.write" is not declared`) || !strings.Contains(err.Error(), `"Reports.View" must look like`) {
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
		{"deals.manage", "deals.discount", true}, // custom actions too
		{"deals.write", "deals.discount", false},
		{"deals.manage", "reports.view", false}, // only its own resource
		{"crm.deals.manage", "crm.deals.write", true},
		{"crm.manage", "crm.deals.write", false}, // not a prefix match
		{"deals.manage", "deals", false},
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
		{Name: "deals.discount", Permissions: []string{"deals.discount"}},
		{Name: "reports.pipeline", Permissions: []string{"reports.view"}},
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
