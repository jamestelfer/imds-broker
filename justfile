# Run all checks before committing
verify: fmt build lint test

# Format all Go source files
fmt:
    gofmt -w .

# Run all tests
test:
    go test ./...

# Build the binary
build:
    mkdir -p dist
    go build -o dist/ ./cmd/imds-broker/...

# Run linter
lint:
    golangci-lint run ./...

# Run tests with verbose output
test-v:
    go test -v ./...

# Build a local snapshot release (no publish)
release-snapshot:
    goreleaser release --snapshot --clean
