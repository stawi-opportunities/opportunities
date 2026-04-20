#!/usr/bin/env bash
#
# Samples N random active canonical jobs and verifies that for each one
# the R2 archive contains: canonical.json, manifest.json, a variant JSON
# per variant in the manifest, and a raw HTML blob per raw_content_hash.
#
# Any drift aborts with non-zero exit. Meant for post-deploy + nightly
# runs as a cheap consistency sentinel between Postgres and R2.
#
# Required env:
#   ARCHIVE_R2_BUCKET    — private archive bucket name
#   ARCHIVE_R2_ENDPOINT  — https://<account>.r2.cloudflarestorage.com
#   PGHOST/PGPORT/PGUSER/PGPASSWORD/PGDATABASE  — standard psql env
#   AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY  — archive bucket creds
#
# Optional:
#   SAMPLE  — number of canonicals to sample (default 50)
#
# Note: SQL values are interpolated into query strings below. Inputs come
# from our own Postgres (cluster_id, variant_id, raw_content_hash) — never
# from user-supplied data — so interpolation is safe in this trusted context.

set -euo pipefail

: "${ARCHIVE_R2_BUCKET:?ARCHIVE_R2_BUCKET required}"
: "${ARCHIVE_R2_ENDPOINT:?ARCHIVE_R2_ENDPOINT required (e.g. https://<account>.r2.cloudflarestorage.com)}"

SAMPLE=${SAMPLE:-50}

echo "archive-verify: sampling $SAMPLE active canonicals..."

# Pull a random sample of canonical ids + their cluster_ids.
sample_file=$(mktemp -t archive-verify-sample.XXXXXX)
trap 'rm -f "$sample_file"' EXIT

psql -v ON_ERROR_STOP=1 -qAt -c "
  SELECT c.id, c.cluster_id
    FROM canonical_jobs c
   WHERE c.status = 'active'
   ORDER BY random()
   LIMIT $SAMPLE
" > "$sample_file"

fail=0
checked=0

while IFS='|' read -r canonical_id cluster_id; do
    if [[ -z "${cluster_id:-}" ]]; then continue; fi
    checked=$((checked + 1))

    # 1. canonical.json present.
    if ! aws s3api head-object \
         --bucket "$ARCHIVE_R2_BUCKET" \
         --key "clusters/$cluster_id/canonical.json" \
         --endpoint-url "$ARCHIVE_R2_ENDPOINT" >/dev/null 2>&1; then
        echo "MISSING canonical.json for cluster=$cluster_id"
        fail=1
        continue
    fi

    # 2. manifest.json present.
    if ! aws s3api head-object \
         --bucket "$ARCHIVE_R2_BUCKET" \
         --key "clusters/$cluster_id/manifest.json" \
         --endpoint-url "$ARCHIVE_R2_ENDPOINT" >/dev/null 2>&1; then
        echo "MISSING manifest.json for cluster=$cluster_id"
        fail=1
        continue
    fi

    # 3. For each variant in DB, verify variant.json + raw blob.
    psql -v ON_ERROR_STOP=1 -qAt -c "
      SELECT id, COALESCE(raw_content_hash, '')
        FROM job_variants
       WHERE cluster_id = '$cluster_id'
    " | while IFS='|' read -r variant_id raw_hash; do
        if [[ -z "${variant_id:-}" ]]; then continue; fi
        if ! aws s3api head-object \
             --bucket "$ARCHIVE_R2_BUCKET" \
             --key "clusters/$cluster_id/variants/$variant_id.json" \
             --endpoint-url "$ARCHIVE_R2_ENDPOINT" >/dev/null 2>&1; then
            echo "MISSING variant.json cluster=$cluster_id variant=$variant_id"
            exit 1
        fi
        if [[ -n "$raw_hash" ]]; then
            if ! aws s3api head-object \
                 --bucket "$ARCHIVE_R2_BUCKET" \
                 --key "raw/$raw_hash.html.gz" \
                 --endpoint-url "$ARCHIVE_R2_ENDPOINT" >/dev/null 2>&1; then
                echo "MISSING raw blob hash=$raw_hash (used by variant=$variant_id)"
                exit 1
            fi
        fi
    done
done < "$sample_file"

if [[ $fail -ne 0 ]]; then
    echo "archive-verify: drift detected; see MISSING lines above."
    exit 1
fi

echo "archive-verify: $checked canonicals checked, all blobs present."
