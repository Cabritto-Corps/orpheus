.PHONY: test vet lint check build fix

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

lint:
	golangci-lint run --timeout=5m

check: fix test vet lint
