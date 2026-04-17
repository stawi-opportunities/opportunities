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
UI_PORT ?= 5170

hugo-build:
	cd ui && hugo --minify

# Runs Hugo with the hugo.toml defaults — which point at the exposed
# Stawi cluster (jobs-api.stawi.org, api.stawi.org, jobs-repo.stawi.org).
# This is the right target for frontend-only work.
ui-dev:
	cd ui && hugo server --bind 0.0.0.0 --port $(UI_PORT)

# For backend-in-the-loop development: overrides the API origins to localhost
# so the UI talks to a locally-running api (:8082) and candidates (:8080).
ui-dev-local:
	cd ui && \
		HUGO_PARAMS_apiURL=http://localhost:8082 \
		HUGO_PARAMS_candidatesAPIURL=http://localhost:8080 \
		hugo server --bind 0.0.0.0 --port $(UI_PORT)

ui-build: hugo-build
	@echo "Static site built at ui/public/"

ui-deps:
	cd ui && npm install
