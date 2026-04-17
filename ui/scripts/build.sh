#!/usr/bin/env bash
# build.sh — produce the final public/ tree. Used by CF Pages.
#   1. install + build the React app (writes into static/app and data/app_manifest.json)
#   2. hugo --minify (picks up the manifest)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
UI_DIR="$(dirname "$SCRIPT_DIR")"

echo "[build] installing app deps..."
(cd "$UI_DIR/app" && npm ci --prefer-offline --no-audit --no-fund)

echo "[build] building Vite app..."
(cd "$UI_DIR/app" && npm run build)

echo "[build] building Hugo site..."
(cd "$UI_DIR" && hugo --minify)

echo "[build] done: $UI_DIR/public/"
