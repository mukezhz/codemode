package registry

import (
	"bytes"
	"encoding/json"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Encodes in the shape the model was shown: proto names, empty lists as [], int64 as numbers.
// ponytail: a 64-bit integer above 2^53 loses precision in JavaScript; declare such IDs as string in the proto.
func MarshalJSON(message proto.Message) (json.RawMessage, error) {
	encoded, err := protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}.Marshal(message)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return json.Marshal(unquote64(value, message.ProtoReflect().Descriptor()))
}

func unquote64(value any, descriptor protoreflect.MessageDescriptor) any {
	object, ok := value.(map[string]any)
	if !ok {
		return value // a well-known type with its own JSON form, such as a Timestamp
	}
	fields := descriptor.Fields()
	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		name := string(field.Name())
		element, present := object[name]
		if !present {
			continue
		}
		switch {
		case field.IsMap():
			if entries, ok := element.(map[string]any); ok {
				for key, entry := range entries {
					entries[key] = unquoteValue(entry, field.MapValue())
				}
			}
		case field.IsList():
			if items, ok := element.([]any); ok {
				for index, item := range items {
					items[index] = unquoteValue(item, field)
				}
			}
		default:
			object[name] = unquoteValue(element, field)
		}
	}
	return object
}

func unquoteValue(value any, field protoreflect.FieldDescriptor) any {
	switch field.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		if name := field.Message().FullName(); name == "google.protobuf.Int64Value" || name == "google.protobuf.UInt64Value" {
			if text, ok := value.(string); ok {
				return json.Number(text)
			}
		}
		return unquote64(value, field.Message())
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		if text, ok := value.(string); ok {
			return json.Number(text)
		}
	}
	return value
}
