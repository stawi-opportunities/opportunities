#!/usr/bin/env bash
#
# Seed a candidate_profiles row for local-dev session-capture testing.
#
# The matching service's /pairings and /candidates/me/sessions/*
# endpoints look up the candidate via profile_id == JWT.sub. After you
# sign into the dev Hydra via the UI, grab the resulting sub claim and
# pass it here so the row exists.
#
# Usage:
#   ./scripts/seed-dev-candidate.sh <hydra-sub-claim>
#
# Reads the sub from "$1" or, when omitted, from stdin.
#
# Idempotent: re-running with the same sub updates the existing row.

set -euo pipefail

cd "$(dirname "$0")/.."

CONTAINER=${PG_CONTAINER:-deploy-postgres-1}
PG_USER=${PG_USER:-stawi}
PG_DB=${PG_DB:-stawijobs}
CANDIDATE_ID=${CANDIDATE_ID:-cnd_dev_1}

if [[ $# -ge 1 ]]; then
    SUB="$1"
else
    echo -n "Hydra sub claim (from your dev sign-in): "
    read -r SUB
fi

if [[ -z "${SUB:-}" ]]; then
    echo "error: sub claim is empty" >&2
    exit 1
fi

docker exec -i "$CONTAINER" psql -U "$PG_USER" -d "$PG_DB" <<SQL
INSERT INTO candidate_profiles (
    id, profile_id, status, subscription, auto_apply,
    current_title, target_job_title, languages, comm_email,
    created_at, updated_at
) VALUES (
    '$CANDIDATE_ID', '$SUB', 'active', 'paid', true,
    'Software Engineer', 'Software Engineer', 'en', true,
    now(), now()
)
ON CONFLICT (id) DO UPDATE SET
    profile_id   = EXCLUDED.profile_id,
    subscription = EXCLUDED.subscription,
    auto_apply   = EXCLUDED.auto_apply,
    updated_at   = now();

SELECT id, profile_id, status, subscription, auto_apply
  FROM candidate_profiles
 WHERE id = '$CANDIDATE_ID';
SQL
