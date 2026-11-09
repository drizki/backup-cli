VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PKG     := github.com/drizki/backup-cli/internal/version
LDFLAGS := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).Date=$(DATE)

.PHONY: build test race cover lint vuln shellcheck fmt vet size complete covergate check

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o backup-cli ./cmd/backup-cli

test:
	go test ./...

race:
	go test -race ./...

cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -n 1

lint:
	golangci-lint run ./...

vuln:
	govulncheck ./...

shellcheck:
	@files="$$(find . -name '*.sh' -not -path './.git/*')"; \
	if [ -n "$$files" ]; then shellcheck $$files; else echo "no shell scripts"; fi

fmt:
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)

vet:
	go vet ./...

size:
	./scripts/check-file-size.sh

complete:
	./scripts/check-completeness.sh

covergate:
	go test -race -coverprofile=coverage.out ./... >/dev/null
	./scripts/check-coverage.sh coverage.out

check: fmt vet size complete covergate lint vuln shellcheck
