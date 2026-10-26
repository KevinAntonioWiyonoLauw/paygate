# Modules
GOMODCACHE_DIR = $(shell go env GOMODCACHE)

.PHONY: help up down logs psql migrate migrate-down dev build test fmt tidy web-install web-dev web-build clean

help:
	@echo "paygate"
	@echo ""
	@echo "  make up            start postgres + adminer"
	@echo "  make down          stop containers (keeps data)"
	@echo "  make reset         stop containers AND drop the data volume"
	@echo "  make psql          open a psql shell inside the container"
	@echo "  make migrate       apply pending migrations"
	@echo "  make dev           run the Go gateway with hot reload off (plain go run)"
	@echo "  make build         compile backend to backend/bin/paygate.exe"
	@echo "  make test          run go test ./..."
	@echo "  make web-install   pnpm install in web/"
	@echo "  make web-dev       run the Next.js dev server"
	@echo "  make web-build     production build of the web app"
	@echo ""

up:
	docker compose up -d
	@echo "waiting for postgres..."
	@docker compose exec -T postgres sh -c 'until pg_isready -U paygate -d paygate; do sleep 1; done'
	@echo "postgres ready on 127.0.0.1:5433  |  adminer on http://127.0.0.1:8081"

down:
	docker compose down

reset:
	docker compose down -v

logs:
	docker compose logs -f --tail=100

psql:
	docker compose exec postgres psql -U paygate -d paygate

migrate:
	cd backend && go run ./cmd/gateway -migrate-only

dev:
	cd backend && go run ./cmd/gateway

build:
	cd backend && go build -o bin/paygate.exe ./cmd/gateway

test:
	cd backend && go test ./... -count=1

fmt:
	cd backend && go fmt ./...

tidy:
	cd backend && go mod tidy

web-install:
	cd web && pnpm install

web-dev:
	cd web && pnpm dev

web-build:
	cd web && pnpm build

clean:
	cd backend && go clean -cache -testcache
