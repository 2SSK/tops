BIN := bin/tops
VERSION ?= $(shell git describe --tags --always --dirty)

# One Temporal version per supported minor; make generate writes catalog-<minor>.json.
CATALOG_VERSIONS := $(shell cat tools/cataloggen/versions.txt)

.PHONY: build test lint vuln check generate clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/2SSK/tops/internal/cli.Version=$(VERSION)" -o $(BIN) ./cmd/tops/

test:
	go test -race -coverprofile=coverage.out ./...
	go -C tools/cataloggen test -race ./...

lint:
	golangci-lint run
	cd tools/cataloggen && golangci-lint run

vuln:
	govulncheck ./...

check: lint test vuln

# The generator is its own module (D23): Temporal's source never enters tops' go.mod.
generate:
	@for v in $(CATALOG_VERSIONS); do	\
				src=.cache/temporal/v$$v;		\
				test -d $$src || git clone --quiet --depth 1 --branch v$$v -c advice.detachedHead=false https://github.com/temporalio/temporal $$src || exit 1;	\
				go -C tools/cataloggen run . -src $(CURDIR)/$$src -out $(CURDIR)/internal/catalog/catalog-$${v%.*}.json || exit 1;	\
	done

clean:
	rm -rf bin coverage.out
