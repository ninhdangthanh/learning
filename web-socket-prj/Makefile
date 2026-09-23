.PHONY: run debug build test tidy

run:
	go run ./cmd/server

debug:
	go run ./cmd/server -debug

build:
	go build -o bin/server ./cmd/server

test:
	go test -race -count=1 ./...

tidy:
	go mod tidy
