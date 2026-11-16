package capability

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Validate checks a program's input against the capability's declared shape
// before any host code sees it. Empty input and null count as {}.
func Validate(item Capability, input json.RawMessage) error {
	// ponytail: a hand-built capability with no input shape (nil Fields) is not checked; every adapter sets Fields.
	if item.Input.Fields == nil {
		return nil
	}
	return ValidateValue(item.Input, item.Types, input)
}

// ValidateValue checks data against schema, resolving named types in types.
// Errors name the path, such as items[2].id, so a model can repair the program.
func ValidateValue(schema TypeSchema, types map[string]TypeSchema, data json.RawMessage) error {
	if trimmed := bytes.TrimSpace(data); len(trimmed) == 0 || string(trimmed) == "null" {
		data = json.RawMessage("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("input is not JSON: %w", err)
	}
	return validator{types: types}.object("", schema, value)
}

type validator struct {
	types map[string]TypeSchema
}

func (check validator) object(path string, schema TypeSchema, value any) error {
	fields, ok := value.(map[string]any)
	if !ok {
		return mismatch(path, "object "+schema.Type, value)
	}
	for _, name := range sortedFields(fields) {
		fieldType, known := schema.Fields[name]
		if !known {
			return fmt.Errorf("%s: unknown field; %s has %s", join(path, name), describe(schema.Type), fieldList(schema))
		}
		if fields[name] == nil {
			continue
		}
		if err := check.value(join(path, name), fieldType, fields[name]); err != nil {
			return err
		}
	}
	for _, name := range schema.Required {
		if fields[name] == nil {
			return fmt.Errorf("%s: required", join(path, name))
		}
	}
	for _, group := range sortedFields(schema.Oneofs) {
		var set []string
		for _, name := range schema.Oneofs[group] {
			if fields[name] != nil {
				set = append(set, name)
			}
		}
		if len(set) > 1 {
			return fmt.Errorf("%s: set at most one of %s, got %s", orInput(path), strings.Join(schema.Oneofs[group], ", "), strings.Join(set, ", "))
		}
	}
	return nil
}

func (check validator) value(path, neutral string, value any) error {
	if element, found := strings.CutPrefix(neutral, "repeated "); found {
		items, ok := value.([]any)
		if !ok {
			return mismatch(path, "list", value)
		}
		for index, item := range items {
			if err := check.value(fmt.Sprintf("%s[%d]", path, index), element, item); err != nil {
				return err
			}
		}
		return nil
	}
	if key, element, found := mapType(neutral); found {
		entries, ok := value.(map[string]any)
		if !ok {
			return mismatch(path, "object", value)
		}
		for _, name := range sortedFields(entries) {
			entryPath := fmt.Sprintf("%s[%q]", path, name)
			if err := mapKey(entryPath, key, name); err != nil {
				return err
			}
			if err := check.value(entryPath, element, entries[name]); err != nil {
				return err
			}
		}
		return nil
	}

	switch neutral {
	case "any":
		return nil
	case "string", "bytes":
		if _, ok := value.(string); !ok {
			return mismatch(path, "string", value)
		}
		return nil
	case "bool":
		if _, ok := value.(bool); !ok {
			return mismatch(path, "bool", value)
		}
		return nil
	case "number":
		if _, ok := value.(json.Number); !ok {
			return mismatch(path, "number", value)
		}
		return nil
	case "integer":
		if !integer(value) {
			return mismatch(path, "integer", value)
		}
		return nil
	}

	named, known := check.types[neutral]
	if !known {
		return fmt.Errorf("%s: type %s is not declared", orInput(path), neutral)
	}
	if len(named.Values) > 0 {
		text, ok := value.(string)
		if !ok || !slices.Contains(named.Values, text) {
			return fmt.Errorf("%s: want one of %s, got %s", orInput(path), strings.Join(named.Values, ", "), show(value))
		}
		return nil
	}
	return check.object(path, named, value)
}

// mapType splits "map<K, V>"; a key is always a scalar, so the first separator splits it.
func mapType(neutral string) (key, value string, found bool) {
	inner, found := strings.CutPrefix(neutral, "map<")
	if !found {
		return "", "", false
	}
	if inner, found = strings.CutSuffix(inner, ">"); !found {
		return "", "", false
	}
	return strings.Cut(inner, ", ")
}

func mapKey(path, key, name string) error {
	switch key {
	case "integer":
		if _, err := strconv.ParseInt(name, 10, 64); err != nil {
			return fmt.Errorf("%s: key must be an integer", path)
		}
	case "number":
		if _, err := strconv.ParseFloat(name, 64); err != nil {
			return fmt.Errorf("%s: key must be a number", path)
		}
	case "bool":
		if name != "true" && name != "false" {
			return fmt.Errorf("%s: key must be true or false", path)
		}
	}
	return nil
}

func integer(value any) bool {
	number, ok := value.(json.Number)
	if !ok {
		return false
	}
	if _, err := number.Int64(); err == nil {
		return true
	}
	float, err := number.Float64()
	return err == nil && float == math.Trunc(float) && !math.IsInf(float, 0)
}

func mismatch(path, want string, value any) error {
	return fmt.Errorf("%s: want %s, got %s", orInput(path), want, show(value))
}

func show(value any) string {
	switch value := value.(type) {
	case nil:
		return "null"
	case string:
		return strconv.Quote(value)
	case json.Number:
		return value.String()
	case bool:
		return strconv.FormatBool(value)
	case []any:
		return "a list"
	case map[string]any:
		return "an object"
	}
	return fmt.Sprintf("%v", value)
}

func join(path, field string) string {
	if path == "" {
		return field
	}
	return path + "." + field
}

func orInput(path string) string {
	if path == "" {
		return "input"
	}
	return path
}

func describe(name string) string {
	if name == "" {
		return "the input"
	}
	return name
}

func fieldList(schema TypeSchema) string {
	if len(schema.Fields) == 0 {
		return "no fields"
	}
	return "fields " + strings.Join(sortedFields(schema.Fields), ", ")
}

func sortedFields[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
