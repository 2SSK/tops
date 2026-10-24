BIN :=bin/tops

.PHONY: build test lint vuln check clean

build:
	CGO_ENABLED=0 go build -trimpath -o $(BIN) ./cmd/tops/

test:
	go test -race -coverprofile=coverage.out ./...

lint:
	golangci-lint run

vuln:
	govulncheck ./...

check: lint test vuln

clean:
	rm -rf bin coverage.out
