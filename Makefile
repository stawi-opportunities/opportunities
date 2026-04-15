SHELL := /bin/bash

SERVICES := source-discovery scheduler worker dedupe search-api ops-control-plane

.PHONY: deps build test run-discovery run-scheduler run-worker run-search run-ops infra-up infra-down migrate

deps:
	go mod tidy

build:
	for svc in $(SERVICES); do \
		go build -o bin/$$svc ./cmd/$$svc; \
	done

test:
	go test ./...

run-discovery:
	go run ./cmd/source-discovery

run-scheduler:
	go run ./cmd/scheduler

run-worker:
	go run ./cmd/worker

run-search:
	go run ./cmd/search-api

run-ops:
	go run ./cmd/ops-control-plane

infra-up:
	docker compose -f deploy/docker-compose.yml up -d

infra-down:
	docker compose -f deploy/docker-compose.yml down -v

migrate:
	psql "$${POSTGRES_DSN}" -f db/migrations/0001_init.sql
