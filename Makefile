.PHONY: generate test

generate:
	mise exec -- buf build --as-file-descriptor-set -o registry/protoset.pb
	go run ./registry

test:
	mise exec -- buf lint
	go test ./...
