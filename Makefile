.PHONY: generate test test-vector

generate:
	mise exec -- buf build --as-file-descriptor-set -o registry/protoset.pb
	go run ./registry

test:
	mise exec -- buf lint
	go test ./...

test-vector:
	test -n "$(SQLITE_VECTOR_EXTENSION)"
	go test ./capability -run TestVectorSearch -count=1
