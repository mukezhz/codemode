package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

const (
	descriptorPath = "registry/protoset.pb"
	registryPath   = "registry/registry.json"
)

var namespacePattern = regexp.MustCompile(`(?m)^\s*code-mode:namespace=([a-z][A-Za-z0-9]*)\s*$`)

type Registry struct {
	Capabilities []Capability `json:"capabilities"`
}

type Capability struct {
	Name        string     `json:"name"`
	RPC         string     `json:"rpc"`
	Description string     `json:"description"`
	Input       TypeSchema `json:"input"`
	Output      TypeSchema `json:"output"`
}

type TypeSchema struct {
	Type   string            `json:"type"`
	Fields map[string]string `json:"fields"`
}

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
	messages := indexMessages(files)
	registry := Registry{Capabilities: []Capability{}}
	seen := map[string]bool{}

	for _, file := range files.File {
		comments := sourceComments(file)
		for serviceIndex, service := range file.Service {
			servicePath := []int32{6, int32(serviceIndex)}
			namespace, err := namespaceFromComment(comments[pathKey(servicePath)])
			if err != nil {
				return Registry{}, fmt.Errorf("%s.%s: %w", file.GetPackage(), service.GetName(), err)
			}

			for methodIndex, method := range service.Method {
				if method.GetClientStreaming() || method.GetServerStreaming() {
					return Registry{}, fmt.Errorf("%s.%s: streaming RPCs are not supported", service.GetName(), method.GetName())
				}

				methodPath := append(append([]int32{}, servicePath...), 2, int32(methodIndex))
				description := strings.TrimSpace(comments[pathKey(methodPath)])
				if description == "" {
					return Registry{}, fmt.Errorf("%s.%s: method description is required", service.GetName(), method.GetName())
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

func sourceComments(file *descriptorpb.FileDescriptorProto) map[string]string {
	comments := map[string]string{}
	for _, location := range file.GetSourceCodeInfo().GetLocation() {
		text := strings.TrimSpace(location.GetLeadingComments())
		if text == "" {
			text = strings.TrimSpace(location.GetTrailingComments())
		}
		comments[pathKey(location.Path)] = text
	}
	return comments
}

func namespaceFromComment(comment string) (string, error) {
	match := namespacePattern.FindStringSubmatch(comment)
	if len(match) != 2 {
		return "", fmt.Errorf("add a service comment like code-mode:namespace=students")
	}
	return match[1], nil
}

func pathKey(path []int32) string {
	parts := make([]string, len(path))
	for index, value := range path {
		parts[index] = fmt.Sprint(value)
	}
	return strings.Join(parts, ".")
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
