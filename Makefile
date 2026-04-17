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

# UI targets — delegate to ui/scripts/{dev,build}.sh which orchestrate Vite
# (React island bundle → static/app/) and Hugo (templates + final /public).

ui-deps:
	cd ui && npm install
	cd ui/app && npm install

# Starts Vite on :5173 and Hugo on :5170 concurrently; ^C stops both.
ui-dev:
	cd ui && npm run dev

# Produces ui/public/ — the artifact CF Pages serves.
ui-build:
	cd ui && npm run build

# Alias kept for muscle memory.
ui-dev-local: ui-dev
