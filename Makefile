.PHONY: dev-up dev-down run-server run-worker migrate-up migrate-down lint test

dev-up:
	docker compose up -d

dev-down:
	docker compose down

run-server:
	go run ./cmd/server

run-worker:
	go run ./cmd/worker

# Requires: go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
migrate-up:
	migrate -path db/migrations -database "$${DATABASE_URL:-postgres://slbus:slbus@localhost:5432/slbus?sslmode=disable}" up

migrate-down:
	migrate -path db/migrations -database "$${DATABASE_URL:-postgres://slbus:slbus@localhost:5432/slbus?sslmode=disable}" down 1

lint:
	golangci-lint run ./...

test:
	go test ./...
