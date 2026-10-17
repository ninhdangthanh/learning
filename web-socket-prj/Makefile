.PHONY: run debug run-once build test tidy

WS_ALLOWED_ORIGINS ?= chrome-extension://gobngblklhkgmjhbpbdlkglbhhlafjnh
ORIGINS ?= $(WS_ALLOWED_ORIGINS)
JWT_SECRET ?= local-dev-only-insecure-jwt-secret-0123456789
export JWT_SECRET

run:
	WS_ALLOWED_ORIGINS="*" go tool air -- -dev

debug:
	WS_ALLOWED_ORIGINS="*" go tool air -- -dev -debug

run-once:
	go run ./cmd/server -dev -origins "*"

build:
	go build -o bin/server ./cmd/server

test:
	go test -race -count=1 ./...

tidy:
	go mod tidy
