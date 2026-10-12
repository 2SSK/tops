BIN :=bin/tops

.PHONY: build test link vuln check clean

build:
	go build -trimpath -o $(BIN) ./cmd/tops/

test:
	go test -race -coverprofile=coverage.out ./...

lint:
	golangci-lint run

vuln:
	govulncheck ./...

check: lint test vuln

clean:
	rm -rf bin coverage.out
