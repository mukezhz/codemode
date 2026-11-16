// Package gosource turns typed Go functions into capabilities: the schema the
// model sees comes from the request and response structs, input is validated
// and decoded before the function runs, and there is no switch to write.
//
//	source, err := gosource.New(
//		gosource.Func("todos.list", "Lists todos", gosource.Public(), app.List, gosource.ReadOnly()),
//		gosource.Func("todos.delete", "Deletes a todo", gosource.Roles("member"), app.Delete, gosource.NeedsApproval()),
//	)
package gosource

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
)

// Access says who may call a capability. There is no zero value: every Func
// names one of Public, Roles or Permissions, so none is exposed by omission.
type Access interface {
	grant() (roles, permissions []string, public bool)
}

type access struct {
	roles, permissions []string
	public             bool
}

func (access access) grant() ([]string, []string, bool) {
	return access.roles, access.permissions, access.public
}

// Public lets every caller call the capability.
func Public() Access { return access{public: true} }

// Roles lets callers holding any of roles call the capability.
func Roles(roles ...string) Access { return access{roles: roles} }

// Permissions lets callers holding any of permissions, such as "deals.write", call the capability.
func Permissions(permissions ...string) Access { return access{permissions: permissions} }

type Option func(*capability.Capability)

// ReadOnly marks a capability that changes nothing. Anything else is treated as a
// write: never cached, never retried after it ran, held in plan mode.
func ReadOnly() Option { return func(item *capability.Capability) { item.ReadOnly = true } }

// NeedsApproval holds the write, and every write after it in the same program,
// until a person approves the plan.
func NeedsApproval() Option { return func(item *capability.Capability) { item.NeedsApproval = true } }

type Definition struct {
	item capability.Capability
	call func(context.Context, json.RawMessage) (json.RawMessage, error)
	err  error
}

var namePattern = regexp.MustCompile(`^[a-z][A-Za-z0-9_]*\.[a-z][A-Za-z0-9_]*$`)

// Func defines the capability name, such as "todos.list", served by handler.
// In and Out must be structs; their fields follow encoding/json, and a desc tag
// describes a field to the model. Errors surface from New.
func Func[In, Out any](name, description string, access Access, handler func(context.Context, In) (Out, error), options ...Option) Definition {
	fail := func(err error) Definition { return Definition{err: fmt.Errorf("gosource: %s: %w", name, err)} }
	if !namePattern.MatchString(name) {
		return fail(fmt.Errorf("name must be namespace.method, matching %s", namePattern))
	}
	if strings.TrimSpace(description) == "" {
		return fail(errors.New("a description is required; it is what search matches and the model reads"))
	}
	if access == nil {
		return fail(errors.New("access is required: gosource.Public(), gosource.Roles(...) or gosource.Permissions(...)"))
	}
	if handler == nil {
		return fail(errors.New("handler is nil"))
	}
	roles, permissions, public := access.grant()
	roles, permissions = trimmed(roles), trimmed(permissions)
	if !public && len(roles) == 0 && len(permissions) == 0 {
		return fail(errors.New("Roles() and Permissions() need at least one name; use Public() to allow everyone"))
	}
	for _, permission := range permissions {
		if err := capability.ValidatePermission(permission); err != nil {
			return fail(err)
		}
	}

	base := typeBase(name)
	input, inputTypes, err := capability.SchemaOf(reflect.TypeFor[In](), base+"Input")
	if err != nil {
		return fail(fmt.Errorf("input: %w", err))
	}
	output, outputTypes, err := capability.SchemaOf(reflect.TypeFor[Out](), base+"Output")
	if err != nil {
		return fail(fmt.Errorf("output: %w", err))
	}
	types := inputTypes
	for typeName, schema := range outputTypes {
		if existing, taken := types[typeName]; taken && !reflect.DeepEqual(existing, schema) {
			return fail(fmt.Errorf("input and output both use the name %s for different types", typeName))
		}
		types[typeName] = schema
	}
	if len(types) == 0 {
		types = nil
	}

	item := capability.Capability{
		Name: name, RPC: name, Description: strings.TrimSpace(description),
		Input: input, Output: output, Types: types,
		Roles: roles, Permissions: permissions,
	}
	for _, option := range options {
		option(&item)
	}
	if item.ReadOnly && item.NeedsApproval {
		return fail(errors.New("a read-only capability cannot need approval"))
	}

	return Definition{item: item, call: func(ctx context.Context, raw json.RawMessage) (output json.RawMessage, err error) {
		var request In
		if err := decode(raw, &request); err != nil {
			return nil, fmt.Errorf("decode input: %w", err)
		}
		defer func() {
			if recovered := recover(); recovered != nil {
				output, err = nil, fmt.Errorf("%s panicked: %v", name, recovered)
			}
		}()
		response, err := handler(ctx, request)
		if err != nil {
			return nil, err
		}
		return encode(response, item.Output, item.Types)
	}}
}

// New collects definitions into a source. Its RPCs are the capability names.
func New(definitions ...Definition) (codemode.Source, error) {
	var errs []error
	calls := map[string]func(context.Context, json.RawMessage) (json.RawMessage, error){}
	shapes := map[string]capability.TypeSchema{}
	registry := capability.Registry{Capabilities: []capability.Capability{}}
	for _, definition := range definitions {
		if definition.err != nil {
			errs = append(errs, definition.err)
			continue
		}
		if _, exists := calls[definition.item.Name]; exists {
			errs = append(errs, fmt.Errorf("gosource: %s is defined twice", definition.item.Name))
			continue
		}
		// The prompt declares each shape once by name, so one name must mean one shape.
		for typeName, schema := range definition.item.Types {
			if existing, taken := shapes[typeName]; taken && !reflect.DeepEqual(existing, schema) {
				errs = append(errs, fmt.Errorf("gosource: %s: another capability already uses the name %s for a different type", definition.item.Name, typeName))
			}
			shapes[typeName] = schema
		}
		calls[definition.item.Name] = definition.call
		registry.Capabilities = append(registry.Capabilities, definition.item)
	}
	if len(definitions) == 0 {
		errs = append(errs, errors.New("gosource: no definitions given"))
	}
	if err := errors.Join(errs...); err != nil {
		return codemode.Source{}, err
	}
	return codemode.Source{
		Registry: registry,
		Execute: func(ctx context.Context, rpc string, input json.RawMessage) (json.RawMessage, error) {
			call, ok := calls[rpc]
			if !ok {
				return nil, fmt.Errorf("gosource: %s is not defined", rpc)
			}
			return call(ctx, input)
		},
	}, nil
}

func decode(raw json.RawMessage, target any) error {
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}

// encode sends nil slices and maps as [] and {}: the prompt promised the model a list.
func encode(response any, schema capability.TypeSchema, types map[string]capability.TypeSchema) (json.RawMessage, error) {
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("encode output: %w", err)
	}
	var value any
	if err := json.Unmarshal(encoded, &value); err != nil {
		return nil, err
	}
	return json.Marshal(fill(value, schema, types))
}

func fill(value any, schema capability.TypeSchema, types map[string]capability.TypeSchema) any {
	object, ok := value.(map[string]any)
	if !ok {
		return value
	}
	for name, fieldType := range schema.Fields {
		object[name] = fillValue(object[name], fieldType, types, schema.Optional, name)
	}
	return object
}

func fillValue(value any, neutral string, types map[string]capability.TypeSchema, optional []string, name string) any {
	if element, found := strings.CutPrefix(neutral, "repeated "); found {
		items, _ := value.([]any)
		if value == nil {
			if slices.Contains(optional, name) {
				return nil
			}
			return []any{}
		}
		for index, item := range items {
			items[index] = fillValue(item, element, types, nil, "")
		}
		return items
	}
	if strings.HasPrefix(neutral, "map<") {
		if value == nil && !slices.Contains(optional, name) {
			return map[string]any{}
		}
		return value
	}
	if nested, known := types[neutral]; known && value != nil {
		return fill(value, nested, types)
	}
	return value
}

func trimmed(values []string) []string {
	var kept []string
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			kept = append(kept, value)
		}
	}
	return kept
}

// typeBase names anonymous structs: "todos.list" becomes "TodosList".
func typeBase(name string) string {
	var builder strings.Builder
	for _, part := range strings.FieldsFunc(name, func(r rune) bool { return r == '.' || r == '_' }) {
		builder.WriteString(strings.ToUpper(part[:1]) + part[1:])
	}
	return builder.String()
}
