package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/mukezhz/codemode/capability"
	"github.com/mukezhz/codemode/codemode"
	"github.com/mukezhz/codemode/llm"
)

// RunAs is Run with a typed answer. The model is told the shape of T, the
// program's return value is checked against it, and a mismatch goes back to the
// model to repair like any failure. T is a struct, following encoding/json as
// gosource does, or a string.
func RunAs[T any](ctx context.Context, mode codemode.CodeMode, model llm.Client, request string, options Options) (T, Result, error) {
	var answer T
	shape, check, err := expect[T]()
	if err != nil {
		return answer, Result{}, err
	}
	if shape != "" {
		options.Instructions = strings.TrimSpace(options.Instructions + "\n" + shape)
	}
	options.check = check
	result, err := Run(ctx, mode, model, request, options)
	if err != nil {
		return answer, result, err
	}
	err = decodeAnswer(result.Answer, &answer)
	return answer, result, err
}

func expect[T any]() (string, func(any) error, error) {
	t := reflect.TypeFor[T]()
	if t.Kind() == reflect.String {
		return "", func(answer any) error {
			if _, ok := answer.(string); !ok {
				return fmt.Errorf("the program must return a string, got %T", answer)
			}
			return nil
		}, nil
	}
	schema, types, err := capability.SchemaOf(t, "Answer")
	if err != nil {
		return "", nil, fmt.Errorf("RunAs needs a struct or string type: %w", err)
	}
	check := func(answer any) error {
		encoded, err := json.Marshal(answer)
		if err != nil {
			return fmt.Errorf("the returned value cannot be encoded: %w", err)
		}
		if err := capability.ValidateValue(schema, types, encoded); err != nil {
			return fmt.Errorf("the returned value does not match %s: %w", schema.Type, err)
		}
		var decoded T
		return decodeAnswer(answer, &decoded)
	}
	return describeShape(schema, types), check, nil
}

func decodeAnswer(answer any, target any) error {
	encoded, err := json.Marshal(answer)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("the returned value does not decode: %w", err)
	}
	return nil
}

// describeShape is dialect-neutral: every engine's model reads it the same way.
func describeShape(schema capability.TypeSchema, types map[string]capability.TypeSchema) string {
	var builder strings.Builder
	builder.WriteString("Instead of a string, return an object of type " + schema.Type + ". Fields marked ? may be left out; every other field is required.\n")
	writeShape(&builder, schema)
	names := make([]string, 0, len(types))
	for name := range types {
		if name != schema.Type {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		writeShape(&builder, types[name])
	}
	return builder.String()
}

func writeShape(builder *strings.Builder, schema capability.TypeSchema) {
	if len(schema.Values) > 0 {
		fmt.Fprintf(builder, "%s is one of %s\n", schema.Type, strings.Join(schema.Values, ", "))
		return
	}
	fields := make([]string, 0, len(schema.Fields))
	for name, neutral := range schema.Fields {
		marker := ""
		if slices.Contains(schema.Optional, name) {
			marker = "?"
		}
		field := name + marker + ": " + neutral
		if description := schema.Descriptions[name]; description != "" {
			field += " (" + description + ")"
		}
		fields = append(fields, field)
	}
	sort.Strings(fields)
	fmt.Fprintf(builder, "%s { %s }\n", schema.Type, strings.Join(fields, ", "))
}
