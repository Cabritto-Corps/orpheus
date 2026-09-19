.PHONY: test vet lint check build fix fmt

build:
	go build -o orpheus ./cmd/orpheus

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

fix:
	go fix ./...

fmt:
	# golangci-lint fmt applies the configured gofmt and goimports
	# formatters; `go fmt` alone cannot fix import grouping/aliasing.
	golangci-lint fmt

lint:
	golangci-lint run --timeout=5m

check: fix fmt test vet lint
