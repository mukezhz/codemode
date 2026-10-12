package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mukezhz/codemode/codemode"
)

type Role string

const (
	RoleMember Role = "member"
	RoleAdmin  Role = "admin"
)

type Identity struct {
	User string
	Role Role
}

func (identity Identity) Admin() bool { return identity.Role == RoleAdmin }

// ErrNoIdentity is a wiring failure, never defaulted, and permanent so the agent does not retry.
var ErrNoIdentity = codemode.Permanent(errors.New("no identity on the request context"))

type contextKey struct{}

func NewContext(ctx context.Context, identity Identity) context.Context {
	return context.WithValue(ctx, contextKey{}, identity)
}

func From(ctx context.Context) (Identity, error) {
	identity, ok := ctx.Value(contextKey{}).(Identity)
	if !ok || identity.User == "" {
		return Identity{}, ErrNoIdentity
	}
	return identity, nil
}

// RequireAdmin returns the identity only for an administrator, failing closed if registry wiring is wrong.
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

// Instructions tell the model whose data it sees, so an empty result is not read as absence.
func (identity Identity) Instructions() string {
	if identity.Admin() {
		return "You are answering for " + identity.User + ", an administrator. " +
			"Capabilities return data for the whole workspace, so totals and reports cover every person unless you filter them."
	}
	return "You are answering for " + identity.User + ", a member. " +
		"Every capability returns only " + identity.User + "'s own tasks — never anyone else's, and never the workspace total. " +
		"You have no way to read another person's work.\n" +
		"If the request asks about someone else, say that it is outside what you can see. " +
		"Do not filter your own tasks by another person's name and report the result as theirs: an empty result means you cannot see their work, not that they have none."
}
