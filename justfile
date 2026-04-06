GOLANGCI_LINT := `which golangci-lint 2>/dev/null || echo "/home/jamestelfer/.local/share/mise/installs/golangci-lint/2.9.0/golangci-lint-2.9.0-linux-arm64/golangci-lint"`

# Run all tests
test:
    go test ./...

# Build the binary
build:
    go build ./cmd/imds-broker/...

# Run linter
lint:
    {{GOLANGCI_LINT}} run ./...

# Run tests with verbose output
test-v:
    go test -v ./...
