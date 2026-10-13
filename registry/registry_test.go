package registry

import (
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestGenerateRegistry(t *testing.T) {
	serviceOptions := &descriptorpb.ServiceOptions{}
	setStringOption(serviceOptions, 50000, "students")
	methodOptions := &descriptorpb.MethodOptions{}
	setStringOption(methodOptions, 50000, "Returns matching students.")

	files := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{{
		Name:    proto.String("codemode.proto"),
		Package: proto.String("codemode"),
		Extension: []*descriptorpb.FieldDescriptorProto{
			{Name: proto.String("namespace"), Number: proto.Int32(50000), Extendee: proto.String(".google.protobuf.ServiceOptions"), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
			{Name: proto.String("description"), Number: proto.Int32(50000), Extendee: proto.String(".google.protobuf.MethodOptions"), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
		},
	}, {
		Package: proto.String("student.v1"),
		MessageType: []*descriptorpb.DescriptorProto{
			{Name: proto.String("Request"), Field: []*descriptorpb.FieldDescriptorProto{{
				Name: proto.String("days"), Type: descriptorpb.FieldDescriptorProto_TYPE_INT32.Enum(),
			}}},
			{Name: proto.String("Response"), Field: []*descriptorpb.FieldDescriptorProto{{
				Name: proto.String("students"), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
				Label: descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
			}}},
		},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name:    proto.String("StudentService"),
			Options: serviceOptions,
			Method: []*descriptorpb.MethodDescriptorProto{{
				Name:       proto.String("InterviewsWithin"),
				InputType:  proto.String(".student.v1.Request"),
				OutputType: proto.String(".student.v1.Response"),
				Options:    methodOptions,
			}},
		}},
	}}}

	registry, err := generateRegistry(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Capabilities) != 1 {
		t.Fatalf("got %d capabilities", len(registry.Capabilities))
	}
	capability := registry.Capabilities[0]
	if capability.Name != "students.interviewsWithin" {
		t.Errorf("name = %q", capability.Name)
	}
	if capability.RPC != "student.v1.StudentService/InterviewsWithin" {
		t.Errorf("rpc = %q", capability.RPC)
	}
	if capability.Input.Fields["days"] != "number" {
		t.Errorf("input days = %q", capability.Input.Fields["days"])
	}
	if capability.Output.Fields["students"] != "repeated string" {
		t.Errorf("output students = %q", capability.Output.Fields["students"])
	}
}

func setStringOption(message proto.Message, number protowire.Number, value string) {
	unknown := protowire.AppendTag(nil, number, protowire.BytesType)
	message.ProtoReflect().SetUnknown(protowire.AppendString(unknown, value))
}

func TestNoSideEffectsMarksACapabilityReadOnly(t *testing.T) {
	serviceOptions := &descriptorpb.ServiceOptions{}
	setStringOption(serviceOptions, 50000, "students")
	read := &descriptorpb.MethodOptions{IdempotencyLevel: descriptorpb.MethodOptions_NO_SIDE_EFFECTS.Enum()}
	setStringOption(read, 50000, "Lists students.")
	write := &descriptorpb.MethodOptions{IdempotencyLevel: descriptorpb.MethodOptions_IDEMPOTENT.Enum()}
	setStringOption(write, 50000, "Enrols a student.")

	files := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{{
		Name:    proto.String("codemode.proto"),
		Package: proto.String("codemode"),
		Extension: []*descriptorpb.FieldDescriptorProto{
			{Name: proto.String("namespace"), Number: proto.Int32(50000), Extendee: proto.String(".google.protobuf.ServiceOptions"), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
			{Name: proto.String("description"), Number: proto.Int32(50000), Extendee: proto.String(".google.protobuf.MethodOptions"), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
		},
	}, {
		Package:     proto.String("student.v1"),
		MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("Empty")}},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name:    proto.String("StudentService"),
			Options: serviceOptions,
			Method: []*descriptorpb.MethodDescriptorProto{
				{Name: proto.String("List"), InputType: proto.String(".student.v1.Empty"), OutputType: proto.String(".student.v1.Empty"), Options: read},
				{Name: proto.String("Enrol"), InputType: proto.String(".student.v1.Empty"), OutputType: proto.String(".student.v1.Empty"), Options: write},
			},
		}},
	}}}

	registry, err := generateRegistry(files)
	if err != nil {
		t.Fatal(err)
	}
	// Idempotent is still a write: only NO_SIDE_EFFECTS may be retried safely.
	if !registry.Capabilities[0].ReadOnly || registry.Capabilities[1].ReadOnly {
		t.Fatalf("read only = %v, %v; want true, false", registry.Capabilities[0].ReadOnly, registry.Capabilities[1].ReadOnly)
	}
}

func TestMalformedPermissionFailsAtLoad(t *testing.T) {
	for _, permission := range []string{"deals.write", "Deals Write"} {
		serviceOptions := &descriptorpb.ServiceOptions{}
		setStringOption(serviceOptions, 50000, "deals")
		methodOptions := &descriptorpb.MethodOptions{}
		setStringOption(methodOptions, 50000, "Moves a deal.")
		methodOptions.ProtoReflect().SetUnknown(append(methodOptions.ProtoReflect().GetUnknown(),
			protowire.AppendString(protowire.AppendTag(nil, 50002, protowire.BytesType), permission)...))

		files := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{{
			Name:    proto.String("codemode.proto"),
			Package: proto.String("codemode"),
			Extension: []*descriptorpb.FieldDescriptorProto{
				{Name: proto.String("namespace"), Number: proto.Int32(50000), Extendee: proto.String(".google.protobuf.ServiceOptions"), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
				{Name: proto.String("description"), Number: proto.Int32(50000), Extendee: proto.String(".google.protobuf.MethodOptions"), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
				{Name: proto.String("permissions"), Number: proto.Int32(50002), Extendee: proto.String(".google.protobuf.MethodOptions"), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
			},
		}, {
			Package:     proto.String("deal.v1"),
			MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("Empty")}},
			Service: []*descriptorpb.ServiceDescriptorProto{{
				Name:    proto.String("DealService"),
				Options: serviceOptions,
				Method: []*descriptorpb.MethodDescriptorProto{{
					Name: proto.String("Move"), InputType: proto.String(".deal.v1.Empty"), OutputType: proto.String(".deal.v1.Empty"), Options: methodOptions,
				}},
			}},
		}}}

		registry, err := generateRegistry(files)
		if permission == "deals.write" {
			if err != nil || len(registry.Capabilities[0].Permissions) != 1 {
				t.Fatalf("valid permission: err = %v, capabilities = %+v", err, registry.Capabilities)
			}
			continue
		}
		// "Deals Write" splits into two names, and "Deals" alone is not resource.action.
		if err == nil {
			t.Fatalf("%q loaded; want an error naming the pattern", permission)
		}
	}
}
