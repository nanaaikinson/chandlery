.PHONY: build test test-integration vet fmt fmt-check lint tidy ci

build:
	go build ./...

test:
	go test ./...

# Requires Docker (testcontainers spins up real Postgres for the db package).
test-integration:
	go test -tags=integration ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

fmt-check:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed on:"; gofmt -l .; exit 1)

# Requires: go install honnef.co/go/tools/cmd/staticcheck@latest
lint:
	staticcheck ./...

tidy:
	go mod tidy

# Fast checks only — no Docker required. What CI runs on every push.
ci: fmt-check vet test
