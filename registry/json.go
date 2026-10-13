package registry

import (
	"bytes"
	"encoding/json"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// MarshalJSON encodes a response in the shape the registry describes to the
// model: proto field names, every field present (an empty list is []), and
// 64-bit integers as numbers. Plain protojson.Marshal uses camelCase names,
// drops empty lists and quotes int64, so a program reading a field it was told
// about would get undefined. Decode requests with protojson.Unmarshal, which
// accepts this shape.
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
		return unquote64(value, field.Message())
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		if text, ok := value.(string); ok {
			return json.Number(text)
		}
	}
	return value
}
