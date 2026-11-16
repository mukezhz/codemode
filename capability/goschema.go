package capability

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// Enum is implemented by a string type that accepts only certain values:
//
//	type Priority string
//	func (Priority) Enum() []string { return []string{"low", "high"} }
type Enum interface {
	Enum() []string
}

// SchemaOf describes the Go struct type t in the neutral form, following
// encoding/json: the json tag names a field and `json:"-"` hides it; a pointer,
// omitempty or omitzero makes it optional and every other field is required;
// embedded structs are flattened. A desc tag describes a field to the model.
// fallback names t when it is anonymous. Types with a shape reflection cannot
// know, such as a custom json.Marshaler, are an error.
func SchemaOf(t reflect.Type, fallback string) (TypeSchema, map[string]TypeSchema, error) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || t == timeType {
		return TypeSchema{}, nil, fmt.Errorf("%s is not a struct", t)
	}
	walker := &goWalker{types: map[string]TypeSchema{}, owners: map[string]reflect.Type{}}
	name, err := walker.typeName(t, fallback)
	if err != nil {
		return TypeSchema{}, nil, err
	}
	walker.owners[name] = t
	walker.root = name
	schema, err := walker.object(t, name)
	if err != nil {
		return TypeSchema{}, nil, err
	}
	if walker.rootReferenced {
		walker.types[name] = schema
	}
	return schema, walker.types, nil
}

var (
	timeType          = reflect.TypeFor[time.Time]()
	rawType           = reflect.TypeFor[json.RawMessage]()
	marshalerType     = reflect.TypeFor[json.Marshaler]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
	enumType          = reflect.TypeFor[Enum]()
	qualifier         = regexp.MustCompile(`[\w./-]*\.`)
)

type goWalker struct {
	types          map[string]TypeSchema
	owners         map[string]reflect.Type
	root           string
	rootReferenced bool
}

func (walker *goWalker) object(t reflect.Type, name string) (TypeSchema, error) {
	schema := TypeSchema{Type: name, Fields: map[string]string{}}
	descriptions := map[string]string{}
	if err := walker.fields(t, name, &schema, descriptions); err != nil {
		return TypeSchema{}, err
	}
	sort.Strings(schema.Optional)
	sort.Strings(schema.Required)
	if len(descriptions) > 0 {
		schema.Descriptions = descriptions
	}
	return schema, nil
}

func (walker *goWalker) fields(t reflect.Type, owner string, schema *TypeSchema, descriptions map[string]string) error {
	for index := range t.NumField() {
		field := t.Field(index)
		tag := field.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, options, _ := strings.Cut(tag, ",")
		if field.Anonymous && name == "" {
			embedded := field.Type
			if embedded.Kind() == reflect.Pointer {
				embedded = embedded.Elem()
			}
			if embedded.Kind() == reflect.Struct {
				if err := walker.fields(embedded, owner, schema, descriptions); err != nil {
					return err
				}
				continue
			}
		}
		if !field.IsExported() {
			continue
		}
		if name == "" {
			name = field.Name
		}
		if _, exists := schema.Fields[name]; exists {
			return fmt.Errorf("%s.%s: two fields encode as %q", owner, field.Name, name)
		}

		kind, err := walker.neutral(field.Type, owner+field.Name)
		if err != nil {
			return fmt.Errorf("%s.%s: %w", owner, field.Name, err)
		}
		if slices.Contains(strings.Split(options, ","), "string") {
			kind = "string"
		}
		schema.Fields[name] = kind

		description := strings.TrimSpace(field.Tag.Get("desc"))
		if description == "" && deref(field.Type) == timeType {
			description = "RFC 3339 timestamp, such as 2026-01-02T15:04:05Z"
		}
		if description != "" {
			descriptions[name] = description
		}

		optional := field.Type.Kind() == reflect.Pointer ||
			slices.Contains(strings.Split(options, ","), "omitempty") ||
			slices.Contains(strings.Split(options, ","), "omitzero")
		if optional {
			schema.Optional = append(schema.Optional, name)
		} else {
			schema.Required = append(schema.Required, name)
		}
	}
	return nil
}

func (walker *goWalker) neutral(t reflect.Type, fallback string) (string, error) {
	t = deref(t)
	switch {
	case t == timeType:
		return "string", nil
	case t == rawType || t.Kind() == reflect.Interface:
		return "any", nil
	case t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8:
		return "bytes", nil
	case t.Kind() == reflect.String && t.Implements(enumType):
		return walker.enum(t, fallback)
	case t.Implements(marshalerType) || reflect.PointerTo(t).Implements(marshalerType):
		return "", fmt.Errorf("%s implements json.Marshaler, so its JSON shape is unknown", t)
	case t.Implements(textMarshalerType) || reflect.PointerTo(t).Implements(textMarshalerType):
		return "string", nil
	}

	switch t.Kind() {
	case reflect.String:
		return "string", nil
	case reflect.Bool:
		return "bool", nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer", nil
	case reflect.Float32, reflect.Float64:
		return "number", nil
	case reflect.Slice, reflect.Array:
		element, err := walker.neutral(t.Elem(), fallback+"Item")
		if err != nil {
			return "", err
		}
		return "repeated " + element, nil
	case reflect.Map:
		key, err := walker.mapKey(t.Key())
		if err != nil {
			return "", err
		}
		value, err := walker.neutral(t.Elem(), fallback+"Value")
		if err != nil {
			return "", err
		}
		return "map<" + key + ", " + value + ">", nil
	case reflect.Struct:
		return walker.named(t, fallback)
	}
	return "", fmt.Errorf("%s cannot be sent as JSON", t)
}

func (walker *goWalker) mapKey(t reflect.Type) (string, error) {
	switch t.Kind() {
	case reflect.String:
		return "string", nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer", nil
	}
	return "", fmt.Errorf("map key %s must be a string or an integer", t)
}

func (walker *goWalker) named(t reflect.Type, fallback string) (string, error) {
	name, err := walker.typeName(t, fallback)
	if err != nil {
		return "", err
	}
	if walker.owners[name] == t {
		walker.rootReferenced = walker.rootReferenced || name == walker.root
		return name, nil
	}
	walker.owners[name] = t
	schema, err := walker.object(t, name)
	if err != nil {
		return "", err
	}
	walker.types[name] = schema
	return name, nil
}

func (walker *goWalker) enum(t reflect.Type, fallback string) (string, error) {
	name, err := walker.typeName(t, fallback)
	if err != nil {
		return "", err
	}
	if walker.owners[name] == t {
		return name, nil
	}
	values := reflect.Zero(t).Interface().(Enum).Enum()
	if len(values) == 0 {
		return "", fmt.Errorf("%s.Enum() returned no values", t)
	}
	walker.owners[name] = t
	walker.types[name] = TypeSchema{Type: name, Values: values}
	return name, nil
}

// typeName is the Go name without package paths; generic arguments keep their short names.
func (walker *goWalker) typeName(t reflect.Type, fallback string) (string, error) {
	name := t.Name()
	if name == "" {
		name = fallback
	}
	name = qualifier.ReplaceAllString(name, "")
	name = strings.NewReplacer("[", "", "]", "", ",", "", " ", "", "*", "").Replace(name)
	if owner, taken := walker.owners[name]; taken && owner != t {
		return "", fmt.Errorf("two different types are both named %s (%s and %s)", name, owner, t)
	}
	return name, nil
}

func deref(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}
