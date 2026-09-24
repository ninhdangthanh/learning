.PHONY: run debug build test tidy

WS_ALLOWED_ORIGINS ?= chrome-extension://gobngblklhkgmjhbpbdlkglbhhlafjnh
ORIGINS ?= $(WS_ALLOWED_ORIGINS)

run:
	go run ./cmd/server -origins "*"

debug:
	go run ./cmd/server -debug -origins "*"

build:
	go build -o bin/server ./cmd/server

test:
	go test -race -count=1 ./...

tidy:
	go mod tidy
