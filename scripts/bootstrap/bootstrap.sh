#!/usr/bin/env bash
# One-shot cluster provisioning for stawi.jobs.
# Run once per environment (new cluster, DR recovery, staging reset).
# Each step is idempotent — re-running is safe.
#
# Usage:
#   cp scripts/bootstrap/vault-seeds.env.example scripts/bootstrap/vault-seeds.env
#   # fill in vault-seeds.env
#   ./scripts/bootstrap/bootstrap.sh
#
# See scripts/bootstrap/README.md for full documentation.
set -euo pipefail

SCRIPT_DIR=$(dirname "$(readlink -f "$0")")

echo "============================================================"
echo "  stawi.jobs cluster bootstrap"
echo "============================================================"

echo ""
echo "[1/4] Seed Vault secrets"
"${SCRIPT_DIR}/seed-vault.sh"

echo ""
echo "[2/4] Apply Postgres migrations"
echo "    stawi.jobs uses GORM AutoMigrate + explicit migration files."
echo "    Apply in order:"
echo "      psql \"\$DATABASE_URL\" -f db/migrations/0001_init.sql"
echo "      psql \"\$DATABASE_URL\" -f db/migrations/0002_materializer_watermark.sql"
echo "      psql \"\$DATABASE_URL\" -f db/migrations/0003_cutover_drop_legacy.sql"
echo "      psql \"\$DATABASE_URL\" -f db/migrations/0004_iceberg_catalog.sql"
echo "    Alternatively, if DO_DATABASE_MIGRATE=true is set, the apps/api"
echo "    service will run AutoMigrate on startup (applies to catalog tables)."
echo "    For a first deploy the explicit psql sequence is recommended."
read -r -p "    Press Enter once migrations are applied (or Ctrl+C to abort)..."

echo ""
echo "[3/4] Create Iceberg namespaces + tables"
"${SCRIPT_DIR}/create-iceberg.sh"

echo ""
echo "[4/4] Create Manticore schema"
"${SCRIPT_DIR}/create-manticore-schema.sh"

echo ""
echo "============================================================"
echo "  Bootstrap complete."
echo "  Next: apply deployments manifests and wait for images."
echo "  See docs/ops/first-deploy-runbook.md for the full checklist."
echo "============================================================"
