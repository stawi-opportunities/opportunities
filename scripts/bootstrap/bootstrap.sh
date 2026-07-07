#!/usr/bin/env bash
# One-shot cluster provisioning for stawi-opportunities/opportunities.
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
echo "  opportunities cluster bootstrap"
echo "============================================================"

echo ""
echo "[1/4] Seed Vault secrets"
"${SCRIPT_DIR}/seed-vault.sh"

echo ""
echo "[2/2] Deploy services with DO_DATABASE_MIGRATE=true"
echo "    The crawler owns the PostgreSQL/TimescaleDB job schema."
echo "    The matching service owns candidate and application schema."
echo "    Wait for both migrations to complete before scaling workers."

echo ""
echo "============================================================"
echo "  Bootstrap complete."
echo "  Next: apply deployments manifests and wait for images."
echo "  Verify GET /admin/crawl/status before enabling crawl schedules."
echo "============================================================"
