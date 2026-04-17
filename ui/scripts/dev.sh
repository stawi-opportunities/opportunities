#!/usr/bin/env bash
# dev.sh — start Vite (5173) + Hugo (5170) concurrently. ^C kills both.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
UI_DIR="$(dirname "$SCRIPT_DIR")"

PIDS=()
cleanup() {
  echo "[dev] shutting down..."
  for pid in "${PIDS[@]}"; do
    kill -- -"$pid" 2>/dev/null || kill "$pid" 2>/dev/null || true
  done
  wait 2>/dev/null || true
}
trap cleanup EXIT INT TERM

echo "[dev] starting Vite on :5173 ..."
(cd "$UI_DIR/app" && npm run dev) &
PIDS+=($!)

echo "[dev] starting Hugo on :5170 ..."
# Development partition OIDC client + localhost callback, so local sign-in
# hits the dev realm (prod defaults live in hugo.toml for CF Pages).
export HUGO_PARAMS_oidcClientID="${HUGO_PARAMS_oidcClientID:-stawi-jobs-web-dev}"
export HUGO_PARAMS_oidcInstallationID="${HUGO_PARAMS_oidcInstallationID:-d7gi6lkpf2t67dlsqrhg}"
export HUGO_PARAMS_oidcRedirectURI="${HUGO_PARAMS_oidcRedirectURI:-http://localhost:5170/auth/callback/}"
(cd "$UI_DIR" && hugo server --bind 0.0.0.0 --port 5170 --disableFastRender) &
PIDS+=($!)

wait
