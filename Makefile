GO ?= go
BINARY_DIR ?= bin

.PHONY: setup fmt lint test build up down logs clean

setup:
	$(GO) mod download

fmt:
	$(GO) fmt ./...

lint:
	test -z "$$(gofmt -l .)"
	$(GO) vet ./...

test:
	$(GO) test -race -coverprofile=coverage.out ./...

build:
	$(GO) build -trimpath -o $(BINARY_DIR)/relay ./cmd/relay
	$(GO) build -trimpath -o $(BINARY_DIR)/demo-receiver ./cmd/demo-receiver
	$(GO) build -trimpath -o $(BINARY_DIR)/healthcheck ./cmd/healthcheck

up:
	test -f .env || cp .env.example .env
	docker compose up --build -d
	docker compose ps

down:
	docker compose down

logs:
	docker compose logs -f relay receiver

clean:
	docker compose down --volumes --remove-orphans
	rm -rf $(BINARY_DIR) coverage.out coverage.html
