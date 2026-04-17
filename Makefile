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

ui-dev:
	cd ui && hugo server --bind 0.0.0.0 --port $(UI_PORT)

ui-build: hugo-build
	@echo "Static site built at ui/public/"

ui-deps:
	cd ui && npm install
