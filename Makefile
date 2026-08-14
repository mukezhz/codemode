.PHONY: generate test

generate:
	protoc -I . --include_imports --include_source_info \
		--descriptor_set_out=registry/protoset.pb proto/*.proto
	go run ./registry

test:
	go test ./...
