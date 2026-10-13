package registry

import (
	"strings"
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

// A method restricted by roles alone must not inherit the service's default
// permissions: that would reopen it to everyone holding them.
func TestMethodAnnotationsReplaceBothServiceDefaults(t *testing.T) {
	str := descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()
	option := func(message proto.Message, number protowire.Number, value string) {
		message.ProtoReflect().SetUnknown(append(message.ProtoReflect().GetUnknown(),
			protowire.AppendString(protowire.AppendTag(nil, number, protowire.BytesType), value)...))
	}
	serviceOptions := &descriptorpb.ServiceOptions{}
	option(serviceOptions, 50000, "deals")
	option(serviceOptions, 50001, "member")     // default_roles
	option(serviceOptions, 50002, "deals.read") // default_permissions
	inherit := &descriptorpb.MethodOptions{}
	option(inherit, 50000, "Lists deals.")
	rolesOnly := &descriptorpb.MethodOptions{}
	option(rolesOnly, 50000, "Deletes every deal.")
	option(rolesOnly, 50001, "admin")
	permissionsOnly := &descriptorpb.MethodOptions{}
	option(permissionsOnly, 50000, "Moves a deal.")
	option(permissionsOnly, 50002, "deals.write")

	files := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{{
		Name: proto.String("codemode.proto"), Package: proto.String("codemode"),
		Extension: []*descriptorpb.FieldDescriptorProto{
			{Name: proto.String("namespace"), Number: proto.Int32(50000), Extendee: proto.String(".google.protobuf.ServiceOptions"), Type: str},
			{Name: proto.String("default_roles"), Number: proto.Int32(50001), Extendee: proto.String(".google.protobuf.ServiceOptions"), Type: str},
			{Name: proto.String("default_permissions"), Number: proto.Int32(50002), Extendee: proto.String(".google.protobuf.ServiceOptions"), Type: str},
			{Name: proto.String("description"), Number: proto.Int32(50000), Extendee: proto.String(".google.protobuf.MethodOptions"), Type: str},
			{Name: proto.String("roles"), Number: proto.Int32(50001), Extendee: proto.String(".google.protobuf.MethodOptions"), Type: str},
			{Name: proto.String("permissions"), Number: proto.Int32(50002), Extendee: proto.String(".google.protobuf.MethodOptions"), Type: str},
		},
	}, {
		Package:     proto.String("deal.v1"),
		MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("Empty")}},
		Service: []*descriptorpb.ServiceDescriptorProto{{Name: proto.String("DealService"), Options: serviceOptions,
			Method: []*descriptorpb.MethodDescriptorProto{
				{Name: proto.String("List"), InputType: proto.String(".deal.v1.Empty"), OutputType: proto.String(".deal.v1.Empty"), Options: inherit},
				{Name: proto.String("PurgeAll"), InputType: proto.String(".deal.v1.Empty"), OutputType: proto.String(".deal.v1.Empty"), Options: rolesOnly},
				{Name: proto.String("Move"), InputType: proto.String(".deal.v1.Empty"), OutputType: proto.String(".deal.v1.Empty"), Options: permissionsOnly},
			}}},
	}}}

	built, err := generateRegistry(files)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, roles, permissions string
	}{
		{"deals.list", "member", "deals.read"}, // declares nothing: inherits both defaults
		{"deals.purgeAll", "admin", ""},        // roles only: no inherited permission
		{"deals.move", "", "deals.write"},      // permissions only: no inherited role
	} {
		for _, item := range built.Capabilities {
			if item.Name != test.name {
				continue
			}
			if got := strings.Join(item.Roles, " "); got != test.roles {
				t.Errorf("%s roles = %q, want %q", test.name, got, test.roles)
			}
			if got := strings.Join(item.Permissions, " "); got != test.permissions {
				t.Errorf("%s permissions = %q, want %q", test.name, got, test.permissions)
			}
		}
	}
}
