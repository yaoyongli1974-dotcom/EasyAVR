# EasyAVR — AI-native video fusion & intelligent video resource platform
SHELL := /bin/bash
SERVER_DIR := server
WEB_DIR := web
BIN := server/bin/easyavr

.PHONY: help deps run build test vet fmt web-dev web-build up down clean db-up db-down run-pg

PG_DSN := postgres://easyavr:easyavr@127.0.0.1:5432/easyavr?sslmode=disable

help: ## show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

deps: ## install Go + web dependencies
	cd $(SERVER_DIR) && go mod download
	cd $(WEB_DIR) && npm install

run: ## run the API server (Go, :18000)
	cd $(SERVER_DIR) && go run ./cmd/easyavr

build: ## build server binary and web assets
	cd $(SERVER_DIR) && go build -o bin/easyavr ./cmd/easyavr
	cd $(WEB_DIR) && npm run build

test: ## run Go tests
	cd $(SERVER_DIR) && go test ./...

vet: ## run go vet
	cd $(SERVER_DIR) && go vet ./...

fmt: ## format Go code
	cd $(SERVER_DIR) && gofmt -w .

web-dev: ## run the web dev server (:5173, proxies /api to :18000)
	cd $(WEB_DIR) && npm run dev

web-build: ## typecheck + build the web app into web/dist
	cd $(WEB_DIR) && npm run build

up: ## start ZLMediaKit (streaming core) via docker compose
	docker compose up -d

down: ## stop ZLMediaKit
	docker compose down

db-up: ## start PostgreSQL (docker compose profile: postgres)
	docker compose --profile postgres up -d postgres

db-down: ## stop PostgreSQL
	docker compose stop postgres

run-pg: ## run the API against the local PostgreSQL container
	cd $(SERVER_DIR) && EASYAVR_DB_DRIVER=postgres EASYAVR_DB_DSN='$(PG_DSN)' go run ./cmd/easyavr

clean: ## remove build artifacts
	rm -rf $(SERVER_DIR)/bin $(WEB_DIR)/dist $(SERVER_DIR)/data
