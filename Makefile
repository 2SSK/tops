BIN := bin/tops
VERSION ?= $(shell git describe --tags --always --dirty)

# One Temporal version per supported minor; make generate writes catalog-<minor>.json.
CATALOG_VERSIONS := $(shell cat tools/cataloggen/versions.txt)

.PHONY: build test cover lint vuln check generate clean lab-up lab-down lab-renew lab-reset

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/2SSK/tops/internal/cli.Version=$(VERSION)" -o $(BIN) ./cmd/tops/

test:
	go test -race -coverprofile=coverage.out ./...
	go -C tools/cataloggen test -race ./...

lint:
	golangci-lint run
	cd tools/cataloggen && golangci-lint run

cover:
	awk -f tools/cover.awk coverage.out

vuln:
	govulncheck ./...

check: lint test cover vuln

# The generator is its own module (D23): Temporal's source never enters tops' go.mod.
generate:
	@for v in $(CATALOG_VERSIONS); do	\
				src=.cache/temporal/v$$v;		\
				test -d $$src || git clone --quiet --depth 1 --branch v$$v -c advice.detachedHead=false https://github.com/temporalio/temporal $$src || exit 1;	\
				go -C tools/cataloggen run . -src $(CURDIR)/$$src -out $(CURDIR)/internal/catalog/catalog-$${v%.*}.json || exit 1;	\
	done

LAB_SECRETS := test/e2e/fleet/environments/lab/secrets.env
LAB_PG := docker compose --env-file $(LAB_SECRETS) -f test/e2e/postgres.compose.yaml

$(LAB_SECRETS):
	mkdir -p $(@D)
	(unmask 077 && printf 'TOPS_PG_PASSWORD=%s\n' "$$(openssl rand -hex 24)" > $@)

lab-up: $(LAB_SECRETS)
	cp test/e2e/vpslab.override.yaml lab/compose.override.yaml
	$(MAKE) -C lab up
	$(LAB_PG) up -d --wait

# vps-pg stays attached to the network, so it goes first or the network cannot be removed.
lab-down: $(LAB_SECRETS)
	$(LAB_PG) down
	$(MAKE) -C lab down

# New nodes and host keys, database kept.
lab-renew:
	$(MAKE) lab-down
	$(MAKE) lab-up

# New nodes and empty database
lab-reset: $(LAB_SECRETS)
	$(LAB_PG) down --volumes
	$(MAKE) lab-renew

clean:
	rm -rf bin coverage.out
