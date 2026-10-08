BINARY  := switchboard
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X github.com/ucgeorge/switchboard/internal/version.Version=$(VERSION) \
           -X github.com/ucgeorge/switchboard/internal/version.Commit=$(COMMIT) \
           -X github.com/ucgeorge/switchboard/internal/version.Date=$(DATE)

.PHONY: build run dev generate test vet install clean check release

# The generated query code is committed, so building needs only Go. Run
# `make generate` after editing anything under internal/db.
build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/switchboard

run: build
	./bin/$(BINARY)

dev:
	go run ./cmd/switchboard serve --headless

generate:
	sqlc generate

test:
	go test ./...

vet:
	go vet ./...

install:
	CGO_ENABLED=0 go install -ldflags "$(LDFLAGS)" ./cmd/switchboard

clean:
	rm -rf bin dist

check:
	go vet ./...
	go test -race -count=1 ./...
	@test -z "$$(gofmt -l cmd internal)"

release:
	python3 scripts/release.py --version "$(VERSION)"
