package registry

import (
	"fmt"

	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

// FromFiles builds a registry from file descriptors that are already compiled
// into your binary.
//
// This is the short path, and usually the right one. Your generated code
// carries its own descriptors, so if you already import the package that
// implements a service, everything needed to describe it is linked in:
//
//	import todov1 "myapp/gen/todo/v1"
//
//	registry, err := registry.FromFiles(todov1.File_todo_v1_todo_proto)
//
// No descriptor set to build, no JSON to generate, embed, or keep in step —
// the registry cannot drift from the contracts because it is derived from them
// at startup. Dependencies are followed automatically, so passing the file that
// declares the service is enough.
//
// Prefer the codemode-registry command instead when you want the registry as a
// reviewable artifact: a diff on registry.json in a pull request is a diff on
// what a model is allowed to call.
func FromFiles(files ...protoreflect.FileDescriptor) (Registry, error) {
	if len(files) == 0 {
		return Registry{}, fmt.Errorf("no file descriptors given")
	}
	set := &descriptorpb.FileDescriptorSet{}
	seen := map[string]bool{}
	for _, file := range files {
		collect(file, set, seen)
	}
	return generateRegistry(set)
}

// FromGlobalFiles builds a registry from every file descriptor linked into the
// binary, which is what protoc-generated packages register from their init.
//
//	registry, err := registry.FromGlobalFiles()
//
// Only services carrying (codemode.namespace) contribute, so linking an
// unrelated protobuf package cannot widen the registry. Even so, FromFiles is
// the safer habit: it says in code exactly which contracts are exposed, and a
// new transitive dependency cannot quietly add to them.
func FromGlobalFiles() (Registry, error) {
	set := &descriptorpb.FileDescriptorSet{}
	seen := map[string]bool{}
	protoregistry.GlobalFiles.RangeFiles(func(file protoreflect.FileDescriptor) bool {
		collect(file, set, seen)
		return true
	})
	if len(set.File) == 0 {
		return Registry{}, fmt.Errorf("no protobuf files are linked into this binary")
	}
	return generateRegistry(set)
}

// collect appends file and everything it imports, depth first and once each.
// Imports matter for two reasons: the request and response messages may live in
// another file, and codemode.proto is where the custom options are declared.
func collect(file protoreflect.FileDescriptor, set *descriptorpb.FileDescriptorSet, seen map[string]bool) {
	path := file.Path()
	if seen[path] {
		return
	}
	seen[path] = true

	imports := file.Imports()
	for i := 0; i < imports.Len(); i++ {
		collect(imports.Get(i).FileDescriptor, set, seen)
	}
	set.File = append(set.File, protodesc.ToFileDescriptorProto(file))
}
