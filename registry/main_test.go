package main

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestGenerateRegistry(t *testing.T) {
	files := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{{
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
			Name: proto.String("StudentService"),
			Method: []*descriptorpb.MethodDescriptorProto{{
				Name:       proto.String("InterviewsWithin"),
				InputType:  proto.String(".student.v1.Request"),
				OutputType: proto.String(".student.v1.Response"),
			}},
		}},
		SourceCodeInfo: &descriptorpb.SourceCodeInfo{Location: []*descriptorpb.SourceCodeInfo_Location{
			{Path: []int32{6, 0}, LeadingComments: proto.String("code-mode:namespace=students")},
			{Path: []int32{6, 0, 2, 0}, LeadingComments: proto.String("Returns matching students.")},
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
	if capability.Output.Fields["students"] != "string[]" {
		t.Errorf("output students = %q", capability.Output.Fields["students"])
	}
}
