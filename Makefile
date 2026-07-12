.PHONY: build test clean install run

BINARY=icaltocaldav
VERSION=$(shell git describe --tags 2>/dev/null || echo "dev")
BUILD=$(shell git rev-parse --short HEAD 2>/dev/null)
LDFLAGS=-ldflags "-X main.version=$(VERSION) -X main.build=$(BUILD)"

build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/icaltocaldav

test:
	go test ./...

clean:
	rm -f $(BINARY)
	rm -rf ~/.local/share/ical-to-caldav

install: build
	install -Dm755 $(BINARY) ~/.local/bin/$(BINARY)

run: build
	./$(BINARY) --config ical-to-caldav.yaml

# For local dev with go.work
go.work:
	go work init .
	go work use ./internal/source
	go work use ./internal/caldav
	go work use ./internal/store
	go work use ./internal/config

.PHONY: vendor
vendor:
	go mod vendor
