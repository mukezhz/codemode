package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/mukezhz/code-mode/capability"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

const (
	descriptorPath = "registry/protoset.pb"
	registryPath   = "registry/registry.json"
)

var namespacePattern = regexp.MustCompile(`^[a-z][A-Za-z0-9]*$`)

type Registry = capability.Registry
type Capability = capability.Capability
type TypeSchema = capability.TypeSchema

func main() {
	data, err := os.ReadFile(descriptorPath)
	if err != nil {
		panic(err)
	}

	var files descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(data, &files); err != nil {
		panic(err)
	}

	registry, err := generateRegistry(&files)
	if err != nil {
		panic(err)
	}

	output, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		panic(err)
	}
	output = append(output, '\n')
	if err := os.WriteFile(registryPath, output, 0o644); err != nil {
		panic(err)
	}

	fmt.Printf("generated %s with %d capabilities\n", registryPath, len(registry.Capabilities))
}

func generateRegistry(files *descriptorpb.FileDescriptorSet) (Registry, error) {
	namespaceOption, err := customOptionNumber(files, ".code_mode.namespace", ".google.protobuf.ServiceOptions")
	if err != nil {
		return Registry{}, err
	}
	descriptionOption, err := customOptionNumber(files, ".code_mode.description", ".google.protobuf.MethodOptions")
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
				return Registry{}, fmt.Errorf("%s.%s: service option (code_mode.namespace) must match %s", file.GetPackage(), service.GetName(), namespacePattern)
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
					return Registry{}, fmt.Errorf("%s.%s: method option (code_mode.description) is required", service.GetName(), method.GetName())
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
				registry.Capabilities = append(registry.Capabilities, Capability{
					Name:        name,
					RPC:         serviceName + "/" + method.GetName(),
					Description: description,
					Input:       input,
					Output:      output,
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

func stringOption(options proto.Message, number protowire.Number) (string, error) {
	if options == nil {
		return "", nil
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
		fieldType, err := scriptType(field, messages)
		if err != nil {
			return TypeSchema{}, fmt.Errorf("%s.%s: %w", typeName, field.GetName(), err)
		}
		fields[field.GetName()] = fieldType
	}
	return TypeSchema{Type: shortTypeName(typeName), Fields: fields}, nil
}

func scriptType(field *descriptorpb.FieldDescriptorProto, messages map[string]*descriptorpb.DescriptorProto) (string, error) {
	if field.GetType() == descriptorpb.FieldDescriptorProto_TYPE_MESSAGE {
		if message := messages[field.GetTypeName()]; message != nil && message.GetOptions().GetMapEntry() {
			if len(message.Field) != 2 {
				return "", fmt.Errorf("invalid map entry")
			}
			key, err := scriptType(message.Field[0], messages)
			if err != nil {
				return "", err
			}
			value, err := scriptType(message.Field[1], messages)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Record<%s, %s>", key, value), nil
		}
	}

	var result string
	switch field.GetType() {
	case descriptorpb.FieldDescriptorProto_TYPE_BOOL:
		result = "boolean"
	case descriptorpb.FieldDescriptorProto_TYPE_STRING:
		result = "string"
	case descriptorpb.FieldDescriptorProto_TYPE_BYTES:
		result = "Uint8Array"
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
		result += "[]"
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
