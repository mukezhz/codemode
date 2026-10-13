package capability

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// PermissionPattern is resource.action, where resource is one or more lowercase
// segments separated by dots and action is read, write, delete or manage:
// deals.write, reports.read, deals.discounts.write. Something an app wants to
// control on its own, such as discounting, is a sub-resource, not a new action.
var PermissionPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*\.(read|write|delete|manage)$`)

const maxPermissionLength = 128

const (
	ActionRead   = "read"
	ActionWrite  = "write"
	ActionDelete = "delete"
	ActionManage = "manage"
)

// Grants reports whether holding one permission satisfies a required one:
// itself; write also grants read on the same resource; manage grants every
// action on its resource and on its sub-resources (deals.manage grants
// deals.discounts.write). Delete grants only delete, so write never implies
// deleting.
func Grants(held, required string) bool {
	if held == required {
		return true
	}
	resource, action := splitPermission(required)
	heldResource, heldAction := splitPermission(held)
	if resource == "" || heldResource == "" {
		return false
	}
	switch heldAction {
	case ActionManage:
		return resource == heldResource || strings.HasPrefix(resource, heldResource+".")
	case ActionWrite:
		return resource == heldResource && action == ActionRead
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
		return fmt.Errorf("permission %q must look like resource.action, with action read, write, delete or manage: lowercase letters, digits and underscores, separated by dots", permission)
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
