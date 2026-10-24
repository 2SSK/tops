BIN := bin/tops
VERSION ?= $(shell git describe --tags --always --dirty)

.PHONY: build test lint vuln check clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/2SSK/tops/internal/cli.Version=$(VERSION)" -o $(BIN) ./cmd/tops/

test:
	go test -race -coverprofile=coverage.out ./...

lint:
	golangci-lint run

vuln:
	govulncheck ./...

check: lint test vuln

clean:
	rm -rf bin coverage.out
