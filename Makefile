.PHONY: build test test-integration pull-images vet fmt fmt-check lint tidy ci

build:
	go build ./...

test:
	go test ./...

# Requires Docker (testcontainers spins up a real Postgres, Redis, MinIO and
# MongoDB). Run pull-images first on a cold machine.
test-integration:
	go test -tags=integration ./...

# Fetch every image the integration suite starts, all at once. Without this
# each package's TestMain pulls its own as it runs, and `go test` only runs a
# few package binaries at a time, so the downloads end up partly serialized —
# on a 2-core CI runner that is most of the wall clock.
#
# The list is a convenience, not a contract, so a pull that fails is a
# warning rather than an error: an image missing here — drifted from the test
# files, withdrawn upstream, or behind a registry having a bad day — is still
# attempted by testcontainers on demand, and it is the test that should report
# a genuinely unavailable image, not this. Pre-pulling only ever saves time.
#
# Withdrawal is not hypothetical: MinIO removed minio/minio from Docker Hub,
# which broke storage/s3 with "repository does not exist" and no change on our
# side. Hence quay.io below.
pull-images:
	@printf '%s\n' \
		postgres:16-alpine \
		redis:7-alpine \
		quay.io/minio/minio:RELEASE.2024-01-16T16-07-38Z \
		mongo:8 \
		testcontainers/ryuk:0.14.0 \
		| xargs -P 5 -n 1 -I{} sh -c 'docker pull --quiet {} || echo "pull-images: skipped {}" >&2' 

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
