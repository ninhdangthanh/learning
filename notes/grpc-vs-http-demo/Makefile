PATH := $(PATH):$(HOME)/go/bin

.PHONY: gen server web test

gen:
	buf generate

server:
	cd server && go run .

web:
	cd web && npm run dev

test:
	cd server && go test ./...
