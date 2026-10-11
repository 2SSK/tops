
GO        ?= go
BINARY    := tops
BUILD_DIR := bin
MAIN      := ./cmd/tops

.DEFAULT_GOAL := help

.PHONY: help build test vet fmt-check check install uninstall clean

help:
	@echo "Available commands:"
	@echo "  make build       - Compile the CLI"
	@echo "  make test        - Run all tests"
	@echo "  make vet         - Run go vet"
	@echo "  make fmt-check   - Check Go formatting"
	@echo "  make check       - Run all checks"
	@echo "  make install     - Install tops to Go's bin directory"
	@echo "  make uninstall   - Remove the installed tops binary"
	@echo "  make clean       - Remove local build artifacts"

build:
	@echo "Building $(BINARY)..."
	@mkdir -p $(BUILD_DIR)
	@$(GO) build -trimpath -o $(BUILD_DIR)/$(BINARY) $(MAIN)

test:
	@echo "Running tests..."
	@$(GO) test ./...

vet:
	@echo "Running go vet..."
	@$(GO) vet ./...

fmt-check:
	@files="$$(find . -type f -name '*.go' -not -path './vendor/*' -exec gofmt -l {} +)"; \
	if [ -n "$$files" ]; then \
		printf 'Go files need formatting:\n%s\n' "$$files"; \
		exit 1; \
	fi

check: fmt-check vet test

install:
	@echo "Installing $(BINARY)..."
	@$(GO) install $(MAIN)

uninstall:
	@echo "Uninstalling $(BINARY)..."
	@GOBIN="$$($(GO) env GOBIN)"; \
	if [ -z "$$GOBIN" ]; then \
		GOBIN="$$($(GO) env GOPATH)/bin"; \
	fi; \
	if [ -f "$$GOBIN/$(BINARY)" ]; then \
		rm -f "$$GOBIN/$(BINARY)"; \
		echo "Removed $$GOBIN/$(BINARY)"; \
	else \
		echo "$(BINARY) is not installed at $$GOBIN"; \
	fi

clean:
	@echo "Cleaning local build artifacts..."
	@rm -rf $(BUILD_DIR)
