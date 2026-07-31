.PHONY: build fmt-check vet test race check

build:
	go build -o ./bin/kalshi ./cmd/kalshi

fmt-check:
	test -z "$$(gofmt -l .)"

vet:
	go vet ./...

test:
	go test ./...

race:
	go test -race ./...

check: fmt-check vet test race
