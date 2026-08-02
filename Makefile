.PHONY: build fmt-check install-check vet test race check

build:
	go build -o ./bin/kalshi ./cmd/kalshi

fmt-check:
	test -z "$$(gofmt -l .)"

install-check:
	sh -n ./install.sh

vet:
	go vet ./...

test:
	go test ./...

race:
	go test -race ./...

check: fmt-check install-check vet test race
