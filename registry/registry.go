// Package registry turns a protobuf descriptor set into a capability registry.
//
// The registry is the allowlist: it maps a capability name a model may call to
// the RPC path the host will invoke. Generating it from descriptors rather than
// from Go code means a service that is not annotated cannot become callable by
// forgetting a line somewhere else.
//
// The codemode-registry command wraps this for build pipelines. Call Generate
// directly to produce a registry in-process, such as from a go:generate program
// or a test that asserts the surface has not grown.
package registry

import (
	"fmt"
	"regexp"
	"strings"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/mukezhz/codemode/capability"
)

var namespacePattern = regexp.MustCompile(`^[a-z][A-Za-z0-9]*$`)

type Registry = capability.Registry
type Capability = capability.Capability
type TypeSchema = capability.TypeSchema

// Generate builds a registry from a Buf descriptor set. Every service carrying
// (codemode.namespace) contributes its methods; a method missing
// (codemode.description) is an error, because the description is the only
// thing search and the model ever see.
func Generate(files *descriptorpb.FileDescriptorSet) (Registry, error) {
	return generateRegistry(files)
}

// Parse unmarshals a Buf descriptor set and generates a registry from it.
func Parse(descriptor []byte) (Registry, error) {
	var files descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(descriptor, &files); err != nil {
		return Registry{}, fmt.Errorf("decode descriptor set: %w", err)
	}
	return generateRegistry(&files)
}

func generateRegistry(files *descriptorpb.FileDescriptorSet) (Registry, error) {
	namespaceOption, err := customOptionNumber(files, ".codemode.namespace", ".google.protobuf.ServiceOptions")
	if err != nil {
		return Registry{}, err
	}
	descriptionOption, err := customOptionNumber(files, ".codemode.description", ".google.protobuf.MethodOptions")
	if err != nil {
		return Registry{}, err
	}

	messages := indexMessages(files)
	registry := Registry{Capabilities: []Capability{}}
	seen := map[string]bool{}

	for _, file := range files.File {
		for _, service := range file.Service {
			namespace, err := stringOption(service.GetOptions(), namespaceOption)
			if err != nil {
				return Registry{}, fmt.Errorf("%s.%s: %w", file.GetPackage(), service.GetName(), err)
			}
			namespace = strings.TrimSpace(namespace)
			if !namespacePattern.MatchString(namespace) {
				return Registry{}, fmt.Errorf("%s.%s: service option (codemode.namespace) must match %s", file.GetPackage(), service.GetName(), namespacePattern)
			}

			for _, method := range service.Method {
				if method.GetClientStreaming() || method.GetServerStreaming() {
					return Registry{}, fmt.Errorf("%s.%s: streaming RPCs are not supported", service.GetName(), method.GetName())
				}

				description, err := stringOption(method.GetOptions(), descriptionOption)
				if err != nil {
					return Registry{}, fmt.Errorf("%s.%s: %w", service.GetName(), method.GetName(), err)
				}
				description = strings.TrimSpace(description)
				if description == "" {
					return Registry{}, fmt.Errorf("%s.%s: method option (codemode.description) is required", service.GetName(), method.GetName())
				}

				name := namespace + "." + lowerFirst(method.GetName())
				if seen[name] {
					return Registry{}, fmt.Errorf("duplicate capability %q", name)
				}
				seen[name] = true

				input, err := schemaFor(method.GetInputType(), messages)
				if err != nil {
					return Registry{}, err
				}
				output, err := schemaFor(method.GetOutputType(), messages)
				if err != nil {
					return Registry{}, err
				}

				serviceName := service.GetName()
				if file.GetPackage() != "" {
					serviceName = file.GetPackage() + "." + serviceName
				}
				// The messages the request and response refer to. A model that
				// cannot see them guesses field names, and a wrong guess reads
				// as undefined rather than failing.
				types := map[string]TypeSchema{}
				for _, root := range []string{method.GetInputType(), method.GetOutputType()} {
					if err := collectTypes(root, messages, types); err != nil {
						return Registry{}, fmt.Errorf("%s: %w", name, err)
					}
				}
				delete(types, input.Type)
				delete(types, output.Type)
				if len(types) == 0 {
					types = nil
				}

				registry.Capabilities = append(registry.Capabilities, Capability{
					Name:        name,
					RPC:         serviceName + "/" + method.GetName(),
					Description: description,
					Input:       input,
					Output:      output,
					Types:       types,
				})
			}
		}
	}

	return registry, nil
}

func customOptionNumber(files *descriptorpb.FileDescriptorSet, name, extendee string) (protowire.Number, error) {
	for _, file := range files.File {
		for _, extension := range file.Extension {
			fullName := "." + strings.Trim(file.GetPackage()+"."+extension.GetName(), ".")
			if fullName != name {
				continue
			}
			if extension.GetExtendee() != extendee || extension.GetType() != descriptorpb.FieldDescriptorProto_TYPE_STRING || extension.GetNumber() < 1 {
				return 0, fmt.Errorf("custom option %s has an invalid definition", name)
			}
			return protowire.Number(extension.GetNumber()), nil
		}
	}
	return 0, fmt.Errorf("custom option %s is not defined", name)
}

// stringOption reads the custom option with the given field number.
//
// It has to handle the option arriving two ways. When the descriptors come from
// a Buf descriptor set, codemode.proto's extensions are not linked into this
// binary, so the option sits in the message's unknown fields as raw wire bytes.
// When the descriptors come from generated Go — the registry.FromFiles path —
// the consumer's generated codemode package registered the extension, so the
// option is resolved and the unknown fields are empty. Reading only one way
// silently produces an empty namespace for half of all callers.
func stringOption(options proto.Message, number protowire.Number) (string, error) {
	if options == nil {
		return "", nil
	}
	if value := resolvedOption(options, number); value != "" {
		return value, nil
	}
	data := options.ProtoReflect().GetUnknown()
	value := ""
	for len(data) > 0 {
		fieldNumber, wireType, tagLength := protowire.ConsumeTag(data)
		if tagLength < 0 {
			return "", protowire.ParseError(tagLength)
		}
		data = data[tagLength:]
		if fieldNumber == number {
			if wireType != protowire.BytesType {
				return "", fmt.Errorf("custom option %d is not a string", number)
			}
			option, length := protowire.ConsumeString(data)
			if length < 0 {
				return "", protowire.ParseError(length)
			}
			value = option
			data = data[length:]
			continue
		}
		length := protowire.ConsumeFieldValue(fieldNumber, wireType, data)
		if length < 0 {
			return "", protowire.ParseError(length)
		}
		data = data[length:]
	}
	return value, nil
}

// resolvedOption reads an extension the runtime already knows how to decode.
func resolvedOption(options proto.Message, number protowire.Number) string {
	value := ""
	proto.RangeExtensions(options, func(extension protoreflect.ExtensionType, current any) bool {
		if extension.TypeDescriptor().Number() != protoreflect.FieldNumber(number) {
			return true
		}
		if text, ok := current.(string); ok {
			value = text
			return false
		}
		return true
	})
	return value
}

func indexMessages(files *descriptorpb.FileDescriptorSet) map[string]*descriptorpb.DescriptorProto {
	messages := map[string]*descriptorpb.DescriptorProto{}
	for _, file := range files.File {
		prefix := "." + file.GetPackage()
		if file.GetPackage() == "" {
			prefix = ""
		}
		for _, message := range file.MessageType {
			indexMessage(messages, prefix, message)
		}
	}
	return messages
}

func indexMessage(messages map[string]*descriptorpb.DescriptorProto, prefix string, message *descriptorpb.DescriptorProto) {
	name := prefix + "." + message.GetName()
	messages[name] = message
	for _, nested := range message.NestedType {
		indexMessage(messages, name, nested)
	}
}

func schemaFor(typeName string, messages map[string]*descriptorpb.DescriptorProto) (TypeSchema, error) {
	message, ok := messages[typeName]
	if !ok {
		return TypeSchema{}, fmt.Errorf("message %q not found", typeName)
	}

	fields := make(map[string]string, len(message.Field))
	for _, field := range message.Field {
		fieldType, err := neutralType(field, messages)
		if err != nil {
			return TypeSchema{}, fmt.Errorf("%s.%s: %w", typeName, field.GetName(), err)
		}
		fields[field.GetName()] = fieldType
	}
	return TypeSchema{Type: shortTypeName(typeName), Fields: fields}, nil
}

// neutralType renders a field in the registry's language-neutral notation,
// which is protobuf's own: bool, string, bytes, number, a message or enum name,
// "repeated T", and "map<K, V>". The registry is data read by three sandbox
// dialects, so it must not commit to any one of their type syntaxes; each
// dialect renders this form into its own in sandbox/prompt.go.
//
// Numeric scalars all collapse to "number" because every one of them crosses
// the bridge as a JSON number.
// collectTypes walks every message reachable from typeName and records its
// shape. Recursion is bounded by the seen set in into, so a message that refers
// to itself — a tree node, a threaded comment — terminates.
func collectTypes(typeName string, messages map[string]*descriptorpb.DescriptorProto, into map[string]TypeSchema) error {
	message, ok := messages[typeName]
	if !ok {
		return fmt.Errorf("message %q not found", typeName)
	}
	short := shortTypeName(typeName)
	if _, visited := into[short]; visited {
		return nil
	}

	schema, err := schemaFor(typeName, messages)
	if err != nil {
		return err
	}
	into[short] = schema

	for _, field := range message.Field {
		if field.GetType() != descriptorpb.FieldDescriptorProto_TYPE_MESSAGE {
			continue
		}
		referenced := field.GetTypeName()
		// A map is a synthetic entry message. Its shape is already carried by
		// the map<K, V> notation, so follow the value and skip the entry.
		if entry := messages[referenced]; entry != nil && entry.GetOptions().GetMapEntry() {
			value := entry.Field[1]
			if value.GetType() != descriptorpb.FieldDescriptorProto_TYPE_MESSAGE {
				continue
			}
			referenced = value.GetTypeName()
		}
		if err := collectTypes(referenced, messages, into); err != nil {
			return err
		}
	}
	return nil
}

func neutralType(field *descriptorpb.FieldDescriptorProto, messages map[string]*descriptorpb.DescriptorProto) (string, error) {
	if field.GetType() == descriptorpb.FieldDescriptorProto_TYPE_MESSAGE {
		if message := messages[field.GetTypeName()]; message != nil && message.GetOptions().GetMapEntry() {
			if len(message.Field) != 2 {
				return "", fmt.Errorf("invalid map entry")
			}
			key, err := neutralType(message.Field[0], messages)
			if err != nil {
				return "", err
			}
			value, err := neutralType(message.Field[1], messages)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("map<%s, %s>", key, value), nil
		}
	}

	var result string
	switch field.GetType() {
	case descriptorpb.FieldDescriptorProto_TYPE_BOOL:
		result = "bool"
	case descriptorpb.FieldDescriptorProto_TYPE_STRING:
		result = "string"
	case descriptorpb.FieldDescriptorProto_TYPE_BYTES:
		result = "bytes"
	case descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, descriptorpb.FieldDescriptorProto_TYPE_ENUM:
		result = shortTypeName(field.GetTypeName())
	case descriptorpb.FieldDescriptorProto_TYPE_DOUBLE,
		descriptorpb.FieldDescriptorProto_TYPE_FLOAT,
		descriptorpb.FieldDescriptorProto_TYPE_INT64,
		descriptorpb.FieldDescriptorProto_TYPE_UINT64,
		descriptorpb.FieldDescriptorProto_TYPE_INT32,
		descriptorpb.FieldDescriptorProto_TYPE_FIXED64,
		descriptorpb.FieldDescriptorProto_TYPE_FIXED32,
		descriptorpb.FieldDescriptorProto_TYPE_UINT32,
		descriptorpb.FieldDescriptorProto_TYPE_SFIXED32,
		descriptorpb.FieldDescriptorProto_TYPE_SFIXED64,
		descriptorpb.FieldDescriptorProto_TYPE_SINT32,
		descriptorpb.FieldDescriptorProto_TYPE_SINT64:
		result = "number"
	default:
		return "", fmt.Errorf("unsupported protobuf type %s", field.GetType())
	}

	if field.GetLabel() == descriptorpb.FieldDescriptorProto_LABEL_REPEATED {
		result = "repeated " + result
	}
	return result, nil
}

func shortTypeName(name string) string {
	if index := strings.LastIndex(name, "."); index >= 0 {
		return name[index+1:]
	}
	return name
}

func lowerFirst(value string) string {
	if value == "" {
		return value
	}
	return strings.ToLower(value[:1]) + value[1:]
}
