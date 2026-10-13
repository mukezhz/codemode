package capability

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
)

// PermissionPattern is resource.action: lowercase segments separated by dots,
// such as deals.write, reports.view or billing.refund_partial.
var PermissionPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

const maxPermissionLength = 128

func ValidatePermission(permission string) error {
	if len(permission) > maxPermissionLength || !PermissionPattern.MatchString(permission) {
		return fmt.Errorf("permission %q must look like resource.action: lowercase letters, digits and underscores, separated by dots", permission)
	}
	return nil
}

// ValidatePermissions checks names an administrator is about to grant: each must
// match PermissionPattern and be one this registry's capabilities accept. Call
// it when a role is saved, so a typo is rejected instead of silently granting
// nothing.
func (registry Registry) ValidatePermissions(permissions []string) error {
	known := registry.Permissions()
	var problems []error
	for _, permission := range permissions {
		if err := ValidatePermission(permission); err != nil {
			problems = append(problems, err)
		} else if !slices.Contains(known, permission) {
			problems = append(problems, fmt.Errorf("permission %q is not declared by any capability", permission))
		}
	}
	return errors.Join(problems...)
}
