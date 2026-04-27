#!/usr/bin/env bash
# Seeds the stawi-opportunities/opportunities Vault paths. Reads secrets from a local .env-like
# file that is NEVER checked in. Usage:
#   cp scripts/bootstrap/vault-seeds.env.example scripts/bootstrap/vault-seeds.env
#   # fill in values
#   ./scripts/bootstrap/seed-vault.sh
set -euo pipefail

SCRIPT_DIR=$(dirname "$(readlink -f "$0")")

if [[ ! -f "${SCRIPT_DIR}/vault-seeds.env" ]]; then
    echo "ERROR: missing ${SCRIPT_DIR}/vault-seeds.env (copy from vault-seeds.env.example and fill values)"
    exit 1
fi
# shellcheck source=/dev/null
source "${SCRIPT_DIR}/vault-seeds.env"

# Validate required variables are set and non-empty.
REQUIRED_VARS=(
    ICEBERG_CATALOG_URI
    R2_ACCOUNT_ID
    R2_ACCESS_KEY_ID
    R2_SECRET_ACCESS_KEY
    R2_ENDPOINT
)
for v in "${REQUIRED_VARS[@]}"; do
    if [[ -z "${!v:-}" ]]; then
        echo "ERROR: ${v} is not set in vault-seeds.env"
        exit 1
    fi
done

# Optional CF Pages deploy hook URL — bundled on the same Vault path
# because it's CF-account-scoped material, not bucket config.
R2_DEPLOY_HOOK_URL="${R2_DEPLOY_HOOK_URL:-}"

echo "Obtaining ServiceAccount token for Vault K8s auth..."
SA_TOKEN=$(kubectl create token external-secrets -n external-secrets --audience=vault --duration=600s)

echo "Seeding Vault paths via vault-openbao-0..."
cat <<SCRIPT | sed "s|__SA_TOKEN__|${SA_TOKEN}|g" \
             | sed "s|__ICEBERG_CATALOG_URI__|${ICEBERG_CATALOG_URI}|g" \
             | sed "s|__R2_ACCOUNT_ID__|${R2_ACCOUNT_ID}|g" \
             | sed "s|__R2_ACCESS_KEY_ID__|${R2_ACCESS_KEY_ID}|g" \
             | sed "s|__R2_SECRET_ACCESS_KEY__|${R2_SECRET_ACCESS_KEY}|g" \
             | sed "s|__R2_ENDPOINT__|${R2_ENDPOINT}|g" \
             | sed "s|__R2_DEPLOY_HOOK_URL__|${R2_DEPLOY_HOOK_URL}|g" \
             | kubectl exec -i -n vault-system vault-openbao-0 -- sh
export BAO_ADDR="https://127.0.0.1:8200"
export BAO_CACERT="/vault/userconfig/vault-ca/ca.crt"
export BAO_TOKEN=\$(bao write -field=token auth/kubernetes/login role=external-secrets jwt="__SA_TOKEN__")

echo "  -> writing iceberg-catalog..."
bao kv put secret/stawi-opportunities/opportunities/common/iceberg-catalog \
    uri="__ICEBERG_CATALOG_URI__"

echo "  -> writing r2-account..."
bao kv put secret/stawi-opportunities/opportunities/common/r2-account \
    r2_account_id="__R2_ACCOUNT_ID__" \
    r2_access_key_id="__R2_ACCESS_KEY_ID__" \
    r2_secret_access_key="__R2_SECRET_ACCESS_KEY__" \
    r2_endpoint="__R2_ENDPOINT__" \
    r2_deploy_hook_url="__R2_DEPLOY_HOOK_URL__"

echo "  -> verifying..."
bao kv get -field=uri secret/stawi-opportunities/opportunities/common/iceberg-catalog >/dev/null
bao kv get -field=r2_endpoint secret/stawi-opportunities/opportunities/common/r2-account >/dev/null
SCRIPT

echo "Vault seeding complete. ExternalSecrets will sync within their refreshInterval (~1h)."
echo "To force immediate sync:"
echo "  kubectl annotate externalsecret iceberg-catalog-credentials-opportunities -n product-opportunities reconcile.external-secrets.io/trigger=\$(date +%s) --overwrite"
echo "  kubectl annotate externalsecret r2-account-credentials-opportunities -n product-opportunities reconcile.external-secrets.io/trigger=\$(date +%s) --overwrite"
