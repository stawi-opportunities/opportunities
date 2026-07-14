SHELL := /bin/bash

APP_DIRS := apps/crawler apps/api apps/worker apps/frontier-worker apps/matching apps/applications

# Pinned Hugo extended for reproducible builds (CF Pages ships an old one).
HUGO_VERSION := 0.160.1
HUGO_BIN     := $(CURDIR)/bin/hugo

.PHONY: deps build test test-integration run-crawler run-api run-worker \
        crawl-once infra-up infra-down \
        ui-deps ui-build ui-dev \
        opportunity-kinds-link

deps:
	go mod tidy

build:
	for app in $(APP_DIRS); do \
		go build -o bin/$$(basename $$app) ./$$app/cmd; \
	done

test:
	go test ./...

# Run integration tests (requires Docker for testcontainers).
# Slow — ~3-5 minutes for the matching+applications phase 1 suite.
test-integration:
	go test -tags integration -count=1 -timeout 10m ./tests/integration/...

run-crawler:
	go run ./apps/crawler/cmd

run-api:
	go run ./apps/api/cmd

run-worker:
	go run ./apps/worker/cmd

# One-shot structured crawl into job_ingest_queue (worker drains to opportunities).
# Examples:
#   make crawl-once ARGS='-all-apis -max-items 100'
#   make crawl-once ARGS='-type remoteok'
crawl-once:
	go run ./cmd/crawl-once $(ARGS)

infra-up:
	docker compose -f deploy/docker-compose.yml up -d

infra-down:
	docker compose -f deploy/docker-compose.yml down -v

# ---- UI -------------------------------------------------------------------
# The UI is two builds glued together: Vite compiles the React app into
# ui/static/app/ and writes ui/data/app_manifest.json; Hugo then renders the
# shell pages and picks up that manifest via site.Data.app_manifest.

ui-deps:
	cd ui       && npm install --no-audit --no-fund
	cd ui/app   && npm install --no-audit --no-fund
	cd ui/admin && npm install --no-audit --no-fund

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
	cd ui       && npm ci --prefer-offline --no-audit --no-fund
	cd ui/app   && npm ci --prefer-offline --no-audit --no-fund
	cd ui/admin && npm ci --prefer-offline --no-audit --no-fund
	cd ui/app   && npm run build
	cd ui/admin && npm run build
	cd ui       && npm run css:build
	cd ui       && $(HUGO_BIN) --minify

# Vite :5173 (app) / :5174 (admin) + Hugo :5171 + CF-redirects proxy :5170.
# Browser entrypoint is always http://localhost:5170/ (proxy applies the
# same splat rewrites as Cloudflare Pages `_redirects` so /jobs/<slug>
# hydrates the detail shell).
# - OIDC: Development partition SPA client (not production d7is…qlp0).
# - API: Vite dev proxy (ui/app/vite.config.ts) avoids browser CORS.
ui-dev: $(HUGO_BIN)
	@trap 'kill 0' EXIT INT TERM; \
		(cd ui/app   && npm run dev) & \
		(cd ui/admin && npm run dev) & \
		(cd ui && \
			HUGO_PARAMS_oidcClientID=d7is2kspf2t7cl19qlpg \
			HUGO_PARAMS_oidcInstallationID=d7gi6lkpf2t67dlsqrhg \
			HUGO_PARAMS_oidcRedirectURI=http://localhost:5170/auth/callback/ \
			HUGO_PARAMS_apiURL=http://localhost:5173/jobs-api \
			HUGO_PARAMS_candidatesAPIURL=http://localhost:5173/candidates-api \
			$(HUGO_BIN) server --bind 127.0.0.1 --port 5171 --disableFastRender) & \
		(cd ui && HUGO_ORIGIN=http://127.0.0.1:5171 LISTEN_PORT=5170 node scripts/local-redirects.mjs) & \
		wait

# ---- Opportunity kinds ----------------------------------------------------
# Stages the opportunity-kinds YAML registry at /tmp/opportunity-kinds for
# local dev. In production these YAMLs ship as a ConfigMap mounted at
# /etc/opportunity-kinds (the OPPORTUNITY_KINDS_DIR default).
opportunity-kinds-link:
	@mkdir -p /tmp/opportunity-kinds
	@cp -f definitions/opportunity-kinds/*.yaml /tmp/opportunity-kinds/
	@echo "Linked opportunity-kinds to /tmp/opportunity-kinds"
