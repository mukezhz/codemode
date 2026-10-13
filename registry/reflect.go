package registry

import (
	"fmt"
	"sort"

	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

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

// Only services carrying (codemode.namespace) contribute.
func FromGlobalFiles() (Registry, error) {
	// RangeFiles order is random; sort so the prompt is byte-stable for caching.
	var files []protoreflect.FileDescriptor
	protoregistry.GlobalFiles.RangeFiles(func(file protoreflect.FileDescriptor) bool {
		files = append(files, file)
		return true
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path() < files[j].Path() })

	set := &descriptorpb.FileDescriptorSet{}
	seen := map[string]bool{}
	for _, file := range files {
		collect(file, set, seen)
	}
	if len(set.File) == 0 {
		return Registry{}, fmt.Errorf("no protobuf files are linked into this binary")
	}
	return generateRegistry(set)
}

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
