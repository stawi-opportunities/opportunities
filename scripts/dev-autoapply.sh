#!/usr/bin/env bash
#
# Single-shot helper to run the autoapply service against local infra
# in dry-run mode. Brings up infra if it isn't already, builds the
# devmode binary, waits for Postgres + NATS to accept connections, and
# then runs the service in the foreground.
#
# Usage:
#   ./scripts/dev-autoapply.sh
#
# Override anything via env. Most useful knobs:
#   DRY_RUN=false   — actually call the submitter (will navigate a real
#                     headless Chromium against the apply URL)
#   DEV_INTENT=false — disable POST /dev/intent endpoint
#
# Once the service prints "service starting", in another terminal:
#
#   curl -X POST localhost:8080/dev/intent \
#        -H 'content-type: application/json' \
#        -d '{
#          "candidate_id":"cnd_local_1",
#          "canonical_job_id":"job_local_1",
#          "apply_url":"https://boards.greenhouse.io/co/jobs/1",
#          "full_name":"Ada Lovelace",
#          "email":"ada@example.com"
#        }'
#
# Stop the service with Ctrl-C; tear down infra with `make infra-down`.

set -euo pipefail

cd "$(dirname "$0")/.."

DATABASE_URL=${DATABASE_URL:-postgres://stawi:stawi@127.0.0.1:5432/stawijobs?sslmode=disable}
NATS_URL=${NATS_URL:-nats://127.0.0.1:4222}
HTTP_PORT=${HTTP_PORT:-:8080}
DRY_RUN=${DRY_RUN:-true}
DEV_INTENT=${DEV_INTENT:-true}
DEV_INSECURE_CV=${DEV_INSECURE_CV:-true}

echo "dev-autoapply: ensuring infra is up..."
if ! docker compose -f deploy/docker-compose.yml ps --status running --quiet | grep -q .; then
    docker compose -f deploy/docker-compose.yml up -d
fi

echo "dev-autoapply: building binary with -tags=devmode..."
go build -tags=devmode -o bin/autoapply ./apps/autoapply/cmd

echo "dev-autoapply: waiting for Postgres on 127.0.0.1:5432..."
for _ in $(seq 1 30); do
    if (echo > /dev/tcp/127.0.0.1/5432) 2>/dev/null; then
        break
    fi
    sleep 1
done

echo "dev-autoapply: waiting for NATS on 127.0.0.1:4222..."
for _ in $(seq 1 30); do
    if (echo > /dev/tcp/127.0.0.1/4222) 2>/dev/null; then
        break
    fi
    sleep 1
done

cat <<EOF

dev-autoapply: starting service
  DATABASE_URL=$DATABASE_URL
  AUTO_APPLY_QUEUE_URL=$NATS_URL/svc.opportunities.autoapply.submit.v1
  AUTO_APPLY_DRY_RUN=$DRY_RUN
  AUTO_APPLY_DEV_INTENT_ENDPOINT=$DEV_INTENT
  AUTO_APPLY_DEV_ALLOW_INSECURE_CV=$DEV_INSECURE_CV
  HTTP_PORT=$HTTP_PORT

In another terminal, fire an intent:

  curl -X POST localhost${HTTP_PORT}/dev/intent \\
       -H 'content-type: application/json' \\
       -d '{"candidate_id":"cnd_local_1","canonical_job_id":"job_local_1","apply_url":"https://boards.greenhouse.io/co/jobs/1","full_name":"Ada Lovelace","email":"ada@example.com"}'

Then check the row:

  psql "$DATABASE_URL" -c 'select id,status,method,submitted_at from candidate_applications;'

EOF

exec env \
    DATABASE_URL="$DATABASE_URL" \
    AUTO_APPLY_QUEUE_URL="$NATS_URL/svc.opportunities.autoapply.submit.v1" \
    AUTO_APPLY_DRY_RUN="$DRY_RUN" \
    AUTO_APPLY_DEV_INTENT_ENDPOINT="$DEV_INTENT" \
    AUTO_APPLY_DEV_ALLOW_INSECURE_CV="$DEV_INSECURE_CV" \
    HTTP_PORT="$HTTP_PORT" \
    ./bin/autoapply
