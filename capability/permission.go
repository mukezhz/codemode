package capability

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// PermissionPattern is resource.action: lowercase segments separated by dots,
// such as deals.write, reports.view or billing.refund_partial.
var PermissionPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

const maxPermissionLength = 128

// The actions with meaning beyond their name. Any other action, such as
// deals.discount, grants only itself, or comes with manage.
const (
	ActionRead   = "read"
	ActionWrite  = "write"
	ActionDelete = "delete"
	ActionManage = "manage"
)

// Grants reports whether holding one permission satisfies a required one:
// itself; write also grants read; manage grants every action on its resource.
// Delete grants only delete, so write never implies deleting.
func Grants(held, required string) bool {
	if held == required {
		return true
	}
	resource, action := splitPermission(required)
	heldResource, heldAction := splitPermission(held)
	if resource == "" || heldResource != resource {
		return false
	}
	switch heldAction {
	case ActionManage:
		return true
	case ActionWrite:
		return action == ActionRead
	}
	return false
}

// splitPermission cuts at the last dot: crm.deals.write is resource crm.deals, action write.
func splitPermission(permission string) (resource, action string) {
	index := strings.LastIndex(permission, ".")
	if index < 0 {
		return "", permission
	}
	return permission[:index], permission[index+1:]
}

func ValidatePermission(permission string) error {
	if len(permission) > maxPermissionLength || !PermissionPattern.MatchString(permission) {
		return fmt.Errorf("permission %q must look like resource.action: lowercase letters, digits and underscores, separated by dots", permission)
	}
	return nil
}

// ValidatePermissions checks names an administrator is about to grant: each must
// match PermissionPattern and be one Permissions offers. Call it when a role is
// saved, so a typo is rejected instead of silently granting nothing.
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
