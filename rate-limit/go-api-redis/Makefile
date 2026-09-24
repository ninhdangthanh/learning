GO ?= go
API_URL ?= http://localhost:8080

.PHONY: test vet fmt build api redis up down logs demo load-test tidy

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -l -w .

build:
	$(GO) build -o bin/api ./cmd/api

api:
	$(GO) run ./cmd/api

redis:
	docker run --rm -p 6379:6379 --name go-api-redis redis:8-alpine

up:
	docker compose up --build -d

down:
	docker compose down -v

logs:
	docker compose logs -f api

demo:
	API_URL=$(API_URL) ./scripts/demo.sh

load-test:
	API_URL=$(API_URL) ./scripts/load-test.sh

tidy:
	$(GO) mod tidy
