SHELL := /bin/bash

APP_DIRS := apps/crawler apps/scheduler apps/api apps/writer apps/materializer apps/worker apps/matching apps/autoapply

# Pinned Hugo extended for reproducible builds (CF Pages ships an old one).
HUGO_VERSION := 0.160.1
HUGO_BIN     := $(CURDIR)/bin/hugo

.PHONY: deps build test run-crawler run-scheduler run-api run-writer run-materializer run-worker \
        infra-up infra-down \
        ui-deps ui-build ui-dev \
        archive-verify \
        opportunity-kinds-link

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

run-writer:
	go run ./apps/writer/cmd

run-materializer:
	go run ./apps/materializer/cmd

run-worker:
	go run ./apps/worker/cmd

infra-up:
	docker compose -f deploy/docker-compose.yml up -d

infra-down:
	docker compose -f deploy/docker-compose.yml down -v

# ---- UI -------------------------------------------------------------------
# The UI is two builds glued together: Vite compiles the React app into
# ui/static/app/ and writes ui/data/app_manifest.json; Hugo then renders the
# shell pages and picks up that manifest via site.Data.app_manifest.

ui-deps:
	cd ui && npm install --no-audit --no-fund
	cd ui/app && npm install --no-audit --no-fund

# Downloads pinned Hugo extended to bin/hugo (Linux x86_64). Both the CF
# Pages build env and the maintainer's workstation are linux-amd64 today, so
# a single archive covers both. Cached after first fetch.
$(HUGO_BIN):
	@mkdir -p bin
	@echo "[hugo] fetching Hugo extended $(HUGO_VERSION)..."
	@curl -fsSL \
		https://github.com/gohugoio/hugo/releases/download/v$(HUGO_VERSION)/hugo_extended_$(HUGO_VERSION)_linux-amd64.tar.gz \
		| tar -xz -C bin hugo
	@chmod +x $(HUGO_BIN)

# Produces ui/public/ — the artifact Cloudflare Pages serves.
# CF Pages build command: `make ui-build` (from repo root).
# Two npm installs: ui/ hosts PostCSS+Tailwind (Hugo's css.PostCSS pipe shells
# out to `npx postcss`); ui/app/ hosts the React app itself.
ui-build: $(HUGO_BIN)
	cd ui     && npm ci --prefer-offline --no-audit --no-fund
	cd ui/app && npm ci --prefer-offline --no-audit --no-fund
	cd ui/app && npm run build
	cd ui     && $(HUGO_BIN) --minify

# Vite on :5173 + Hugo on :5170 concurrently. ^C stops both. OIDC env vars
# swap in the Development partition client so local sign-in doesn't hit the
# production realm.
ui-dev:
	@trap 'kill 0' EXIT INT TERM; \
		(cd ui/app && npm run dev) & \
		HUGO_PARAMS_oidcClientID=opportunities-web-dev \
		HUGO_PARAMS_oidcInstallationID=d7gi6lkpf2t67dlsqrhg \
		HUGO_PARAMS_oidcRedirectURI=http://localhost:5170/auth/callback/ \
		(cd ui && hugo server --bind 0.0.0.0 --port 5170 --disableFastRender) & \
		wait

# ---- QA -------------------------------------------------------------------
# Post-deploy + nightly sentinel: samples active canonicals and checks that
# each has canonical.json, manifest.json, per-variant JSON, and raw blobs in
# the R2 archive bucket. Non-zero exit on any drift. See scripts/archive-verify.sh
# for required env (R2_ARCHIVE_BUCKET, R2_ENDPOINT, PG*, AWS_*).
archive-verify:
	@scripts/archive-verify.sh

# ---- Opportunity kinds ----------------------------------------------------
# Stages the opportunity-kinds YAML registry at /tmp/opportunity-kinds for
# local dev. In production these YAMLs ship as a ConfigMap mounted at
# /etc/opportunity-kinds (the OPPORTUNITY_KINDS_DIR default).
opportunity-kinds-link:
	@mkdir -p /tmp/opportunity-kinds
	@cp -f definitions/opportunity-kinds/*.yaml /tmp/opportunity-kinds/
	@echo "Linked opportunity-kinds to /tmp/opportunity-kinds"
