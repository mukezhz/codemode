package registry

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	fixturev1 "github.com/mukezhz/codemode/registry/internal/fixturepb/gen/fixture/v1"
)

func FuzzParse(f *testing.F) {
	set := &descriptorpb.FileDescriptorSet{}
	collect(fixturev1.File_orders_proto, set, map[string]bool{})
	seed, err := proto.Marshal(set)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, descriptor []byte) {
		built, err := Parse(descriptor)
		if err != nil {
			return
		}
		for _, item := range built.Capabilities {
			if item.Name == "" || item.RPC == "" {
				t.Fatalf("capability without a name or rpc: %+v", item)
			}
		}
	})
}
