.PHONY: test vet lint check build

build:
	go build -o orpheus ./cmd/orpheus

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

lint:
	golangci-lint run --timeout=5m

check: test vet lint
