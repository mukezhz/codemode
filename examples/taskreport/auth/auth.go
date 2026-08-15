// Package auth carries who is asking.
//
// It is the answer to the second half of "how do I scope this per user": the
// identity travels on the context, never in a capability argument. That is not
// a stylistic choice. A program written by a model is untrusted input, and any
// field the program can set is a field it can set to someone else's name. If
// tasks.list took an owner, the first thing to try would be someone else's.
//
// So the protos have no "who is asking" field at all, and every branch of
// ExecuteFunc reads the identity from here.
package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Role decides which capabilities exist for a caller and how widely their
// queries reach.
type Role string

const (
	// RoleMember sees and changes only their own tasks.
	RoleMember Role = "member"
	// RoleAdmin sees the whole workspace.
	RoleAdmin Role = "admin"
)

// Identity is the signed-in user. In a real service this is built from a
// verified token in the transport layer, well before code mode is involved.
type Identity struct {
	User string
	Role Role
}

// Admin reports whether this identity may reach across owners.
func (identity Identity) Admin() bool { return identity.Role == RoleAdmin }

// ErrNoIdentity reports that a request reached the application without one.
//
// This is a failure, never a fallback to an anonymous or privileged default.
// A missing identity means the wiring is wrong, and the safe response to wrong
// wiring is to stop.
var ErrNoIdentity = errors.New("no identity on the request context")

type contextKey struct{}

// NewContext returns a context carrying identity.
func NewContext(ctx context.Context, identity Identity) context.Context {
	return context.WithValue(ctx, contextKey{}, identity)
}

// From returns the identity on ctx.
func From(ctx context.Context) (Identity, error) {
	identity, ok := ctx.Value(contextKey{}).(Identity)
	if !ok || identity.User == "" {
		return Identity{}, ErrNoIdentity
	}
	return identity, nil
}

// RequireAdmin returns the identity only when it is an administrator.
//
// The registry already withholds admin capabilities from everyone else, so
// reaching this check means either an administrator is calling or something
// upstream is wrong. It is here anyway: the registry is built per request from
// a role, and a bug in that wiring should fail closed rather than quietly widen
// what a member can see.
func RequireAdmin(ctx context.Context, capability string) (Identity, error) {
	identity, err := From(ctx)
	if err != nil {
		return Identity{}, err
	}
	if !identity.Admin() {
		return Identity{}, fmt.Errorf("%s requires the admin role, and %s is a %s", capability, identity.User, identity.Role)
	}
	return identity, nil
}

// ParseRole validates a role name.
func ParseRole(role string) (Role, error) {
	switch Role(strings.ToLower(strings.TrimSpace(role))) {
	case RoleMember, "":
		return RoleMember, nil
	case RoleAdmin:
		return RoleAdmin, nil
	default:
		return "", fmt.Errorf("role %q is not member or admin", role)
	}
}
