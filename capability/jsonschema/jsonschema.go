// Package jsonschema converts JSON Schema objects into capability.TypeSchema.
// Constructs the neutral form cannot express are errors, never "any", which would mislead the model.
package jsonschema

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/mukezhz/codemode/capability"
)

type schema struct {
	Type        any                `json:"type"`
	Description string             `json:"description"`
	Properties  map[string]*schema `json:"properties"`
	Required    []string           `json:"required"`
	Items       *schema            `json:"items"`
	Enum        []any              `json:"enum"`
}

// Object converts raw, a top-level object schema, into a shape named name.
// Nested objects and enums are added to types, named after their path.
func Object(raw any, name string, types map[string]capability.TypeSchema) (capability.TypeSchema, error) {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return capability.TypeSchema{}, err
	}
	var parsed schema
	if err := json.Unmarshal(encoded, &parsed); err != nil {
		return capability.TypeSchema{}, err
	}
	if kind, _ := single(parsed.Type); kind != "object" {
		return capability.TypeSchema{}, fmt.Errorf("top-level schema is %v, not an object", parsed.Type)
	}
	return fields(&parsed, name, types)
}

func fields(parsed *schema, name string, types map[string]capability.TypeSchema) (capability.TypeSchema, error) {
	result := capability.TypeSchema{Type: name, Fields: map[string]string{}}
	descriptions := map[string]string{}
	for field, property := range parsed.Properties {
		kind, err := typeOf(property, name+Pascal(field), types)
		if err != nil {
			return capability.TypeSchema{}, fmt.Errorf("%s: %w", field, err)
		}
		result.Fields[field] = kind
		if description := strings.TrimSpace(property.Description); description != "" {
			descriptions[field] = description
		}
		if slices.Contains(parsed.Required, field) {
			result.Required = append(result.Required, field)
		} else {
			result.Optional = append(result.Optional, field)
		}
	}
	sort.Strings(result.Optional)
	sort.Strings(result.Required)
	if len(descriptions) > 0 {
		result.Descriptions = descriptions
	}
	return result, nil
}

func typeOf(property *schema, name string, types map[string]capability.TypeSchema) (string, error) {
	if property == nil {
		return "", errors.New("no schema")
	}
	kind, err := single(property.Type)
	if err != nil {
		return "", err
	}
	switch kind {
	case "string":
		if len(property.Enum) == 0 {
			return "string", nil
		}
		values := make([]string, len(property.Enum))
		for i, value := range property.Enum {
			text, ok := value.(string)
			if !ok {
				return "", fmt.Errorf("enum value %v is not a string", value)
			}
			values[i] = text
		}
		types[name] = capability.TypeSchema{Type: name, Values: values}
		return name, nil
	case "integer":
		return "integer", nil
	case "number":
		return "number", nil
	case "boolean":
		return "bool", nil
	case "array":
		element, err := typeOf(property.Items, name+"Item", types)
		if err != nil {
			return "", fmt.Errorf("items: %w", err)
		}
		return "repeated " + element, nil
	case "object":
		nested, err := fields(property, name, types)
		if err != nil {
			return "", err
		}
		types[name] = nested
		return name, nil
	}
	return "", fmt.Errorf("unsupported type %v", property.Type)
}

// single accepts "string" or ["string", "null"]: nullable is still one type.
func single(kind any) (string, error) {
	switch kind := kind.(type) {
	case string:
		return kind, nil
	case []any:
		var found []string
		for _, item := range kind {
			if name, ok := item.(string); ok && name != "null" {
				found = append(found, name)
			}
		}
		if len(found) == 1 {
			return found[0], nil
		}
	}
	return "", fmt.Errorf("type %v is not a single JSON type", kind)
}

// Pascal turns "list_issues" or "list-issues" into "ListIssues", for type names.
func Pascal(name string) string {
	var builder strings.Builder
	upper := true
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			upper = true
			continue
		}
		if upper {
			r = unicode.ToUpper(r)
			upper = false
		}
		builder.WriteRune(r)
	}
	return builder.String()
}
