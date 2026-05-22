#!/usr/bin/env bash
#
# Local-dev shortcut to obtain an extension pairing code WITHOUT
# signing into OIDC.
#
# Exploits the fact that matching's session-capture endpoints trust
# the Bearer JWT's sub claim without verifying the signature
# (gateway-verification model, see pkg/authz/jwt.go). For local-only
# testing we forge a JWT with a known sub, seed a candidate_profiles
# row with that profile_id, then call /pairings to get a code the
# extension can redeem.
#
# Usage:
#   ./scripts/dev-pair.sh
#
# Optional env:
#   DEV_PROFILE_ID  (default: local-test-user)
#   CANDIDATE_ID    (default: cnd_dev_1)
#   MATCHING_URL    (default: http://localhost:8082)
#   PG_CONTAINER    (default: deploy-postgres-1)
#
# The candidate row is provisioned idempotently. Re-run any time —
# each call burns a fresh pair code.

set -euo pipefail

PROFILE_ID=${DEV_PROFILE_ID:-local-test-user}
CANDIDATE_ID=${CANDIDATE_ID:-cnd_dev_1}
MATCHING_URL=${MATCHING_URL:-http://localhost:8082}
PG_CONTAINER=${PG_CONTAINER:-deploy-postgres-1}

# Forge a minimal RS256-shaped token. The header + payload are real
# base64url-encoded JSON; the signature is junk. Matching's
# profileIDFromJWT only base64-decodes the payload and reads `sub`.
b64url() { base64 -w0 | tr '+/' '-_' | tr -d '='; }

header_b64=$(printf '{"alg":"none","typ":"JWT"}' | b64url)
payload_b64=$(printf '{"sub":"%s","iss":"local-dev","aud":"stawi"}' "$PROFILE_ID" | b64url)
JWT="${header_b64}.${payload_b64}.dev-signature"

# Seed the candidate row so profile_id resolves to a real candidate.
docker exec -i "$PG_CONTAINER" psql -U stawi -d stawijobs -q <<SQL >/dev/null
INSERT INTO candidate_profiles (
    id, profile_id, status, subscription, auto_apply,
    current_title, target_job_title, languages, comm_email,
    created_at, updated_at
) VALUES (
    '$CANDIDATE_ID', '$PROFILE_ID', 'active', 'paid', true,
    'Software Engineer', 'Software Engineer', 'en', true,
    now(), now()
)
ON CONFLICT (id) DO UPDATE SET
    profile_id = EXCLUDED.profile_id,
    updated_at = now();
SQL

echo "candidate row OK: id=$CANDIDATE_ID profile_id=$PROFILE_ID"
echo ""

# Mint the pair code.
response=$(curl -sS -X POST "${MATCHING_URL}/pairings" \
    -H "Authorization: Bearer ${JWT}" \
    -H "content-type: application/json" \
    -d '{}')

code=$(echo "$response" | sed -n 's/.*"code":"\([^"]*\)".*/\1/p')

if [[ -z "$code" ]]; then
    echo "error: no pair code in response:" >&2
    echo "$response" >&2
    exit 1
fi

echo "═══════════════════════════════════════════"
echo "  PAIR CODE:   $code"
echo "═══════════════════════════════════════════"
echo ""
echo "Steps:"
echo "  1. Click the Stawi extension icon in your toolbar"
echo "  2. Paste $code into the popup"
echo "  3. Click 'Pair'"
echo ""
echo "Expires in 5 minutes."
