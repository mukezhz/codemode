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
