SHELL := /bin/bash

APP_DIRS := apps/crawler apps/scheduler apps/api

.PHONY: deps build test run-crawler run-scheduler run-api infra-up infra-down

deps:
	go mod tidy

build:
	for app in $(APP_DIRS); do \
		go build -o bin/$$(basename $$app) ./$$app/cmd; \
	done

test:
	go test ./...

run-crawler:
	go run ./apps/crawler/cmd

run-scheduler:
	go run ./apps/scheduler/cmd

run-api:
	go run ./apps/api/cmd

infra-up:
	docker compose -f deploy/docker-compose.yml up -d

infra-down:
	docker compose -f deploy/docker-compose.yml down -v

# UI targets
sitegen:
	go run ./apps/sitegen/cmd --output-dir ui/data

hugo-build: sitegen
	cd ui && chmod +x scripts/sync-r2.sh && ./scripts/sync-r2.sh; hugo --minify

r2-backfill:
	go run ./apps/sitegen/cmd --r2-upload

pagefind: hugo-build
	cd ui && npx pagefind --site public --glob "jobs/**/*.html"

ui-dev:
	cd ui && hugo server --bind 0.0.0.0 --port 1313

ui-build: pagefind
	@echo "Static site built at ui/public/"

ui-deps:
	cd ui && npm install
