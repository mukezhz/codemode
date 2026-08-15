.PHONY: demo-todo generate test test-vector test-postgres docs docs-build docs-preview

demo-todo: generate
	go run ./examples/todo

generate:
	mise exec -- buf build --as-file-descriptor-set -o registry/protoset.pb
	go run ./registry

test:
	mise exec -- buf lint
	go test ./...

test-vector:
	test -n "$(SQLITE_VECTOR_EXTENSION)"
	go test ./capability -run TestVectorSearch -count=1

# Exercise the pgvector backend against a throwaway database.
test-postgres:
	docker run -d --rm --name codemode-pg -e POSTGRES_PASSWORD=test \
		-e POSTGRES_DB=codemode -p 55432:5432 pgvector/pgvector:pg17
	sleep 4
	CAPABILITY_TEST_POSTGRES_DSN=postgres://postgres:test@localhost:55432/codemode?sslmode=disable \
		go test ./capability -run TestPostgresSearch -count=1; \
		status=$$?; docker stop codemode-pg >/dev/null; exit $$status

# Documentation site. Self-contained under docs/: its own package.json and
# lockfile, its own node_modules. The Go module needs none of it.
docs: docs/node_modules
	pnpm --dir docs dev

docs-build: docs/node_modules
	pnpm --dir docs build

docs-preview: docs-build
	pnpm --dir docs preview

docs/node_modules: docs/package.json docs/pnpm-lock.yaml
	pnpm --dir docs install --frozen-lockfile
	@touch docs/node_modules
