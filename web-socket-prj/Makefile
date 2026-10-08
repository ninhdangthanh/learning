.PHONY: run debug run-once build test tidy

WS_ALLOWED_ORIGINS ?= chrome-extension://gobngblklhkgmjhbpbdlkglbhhlafjnh
ORIGINS ?= $(WS_ALLOWED_ORIGINS)

run:
	WS_ALLOWED_ORIGINS="*" go tool air

debug:
	WS_ALLOWED_ORIGINS="*" go tool air -- -debug

run-once:
	go run ./cmd/server -origins "*"

build:
	go build -o bin/server ./cmd/server

test:
	go test -race -count=1 ./...

tidy:
	go mod tidy
