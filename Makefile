.PHONY: demo-todo demo-report demo-web generate generate-go generate-registry test test-vector \
	test-postgres test-consumer test-env docs docs-build docs-preview

demo-todo: generate
	go run ./examples/todo

# A complete application: SQLite tasks, 18 capabilities, reporting. Runs without
# credentials; pass BASE_URL and API_KEY to have a model write the program.
# The web app. Builds the frontend if pnpm is available, then serves.
demo-web: generate
	mise exec -- pnpm --dir examples/taskreport/web install --frozen-lockfile
	mise exec -- pnpm --dir examples/taskreport/web build
	go run ./examples/taskreport -serve :8080 $(if $(BASE_URL),-base-url $(BASE_URL)) \
		$(if $(API_KEY),-api-key $(API_KEY)) $(if $(MODEL),-model $(MODEL))

demo-report: generate
	go run ./examples/taskreport $(if $(BASE_URL),-base-url $(BASE_URL)) \
		$(if $(API_KEY),-api-key $(API_KEY)) $(if $(MODEL),-model $(MODEL)) \
		$(if $(REQUEST),-request "$(REQUEST)")

# Generated Go carries its own descriptors, which is what lets a consumer call
# registry.FromFiles and skip the registry.json step entirely. The registry
# target still runs, because the two paths must agree and a test asserts it.
generate: generate-go generate-registry

generate-go:
	mise exec -- buf generate

# Demonstrates the file-based path for the same contracts registry.FromFiles
# reads at runtime. A test asserts the two agree, so this is not decoration.
generate-registry:
	mise exec -- buf build examples/taskreport/proto \
		--as-file-descriptor-set -o examples/taskreport/protoset.pb
	go run ./cmd/codemode-registry \
		-descriptor examples/taskreport/protoset.pb \
		-out examples/taskreport/registry.json

test:
	mise exec -- buf lint
	go test ./...
	./scripts/check-no-env.sh
	./scripts/check-consumer.sh

test-vector:
	test -n "$(SQLITE_VECTOR_EXTENSION)"
	go test ./capability/sqlitestore -run TestVectorSearch -count=1

# Verify a consumer links only what it blank-imports, and that the core
# packages still build without cgo.
test-consumer:
	./scripts/check-consumer.sh

# Verify no library package reads the environment outside its env.go.
test-env:
	./scripts/check-no-env.sh

# Exercise the pgvector backend against a throwaway database.
test-postgres:
	docker run -d --rm --name codemode-pg -e POSTGRES_PASSWORD=test \
		-e POSTGRES_DB=codemode -p 55432:5432 pgvector/pgvector:pg17
	sleep 4
	CAPABILITY_TEST_POSTGRES_DSN=postgres://postgres:test@localhost:55432/codemode?sslmode=disable \
		go test ./capability/pgstore -run TestPostgresSearch -count=1; \
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
