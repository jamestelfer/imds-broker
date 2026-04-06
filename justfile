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
    go build ./cmd/imds-broker/...

# Run linter
lint:
    golangci-lint run ./...

# Run tests with verbose output
test-v:
    go test -v ./...
