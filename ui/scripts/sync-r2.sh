#!/bin/bash
set -euo pipefail

# Pre-build script for Cloudflare Pages: syncs content from R2 bucket.
# Environment variables required:
#   AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, R2_ACCOUNT_ID, R2_BUCKET

if [ -z "${R2_ACCOUNT_ID:-}" ]; then
    echo "R2_ACCOUNT_ID not set, skipping R2 sync (using local content)"
    exit 0
fi

R2_ENDPOINT="https://${R2_ACCOUNT_ID}.r2.cloudflarestorage.com"
BUCKET="${R2_BUCKET:-stawi-jobs-content}"

echo "Installing AWS CLI..."
pip install awscli --quiet 2>/dev/null

echo "Syncing job content from R2 (s3://${BUCKET}/jobs/)..."
mkdir -p content/jobs
aws s3 sync "s3://${BUCKET}/jobs/" content/jobs/ \
    --endpoint-url "$R2_ENDPOINT" --quiet

echo "Syncing category content from R2..."
mkdir -p content/categories
aws s3 sync "s3://${BUCKET}/categories/" content/categories/ \
    --endpoint-url "$R2_ENDPOINT" --quiet

echo "Syncing data files from R2..."
mkdir -p data
aws s3 cp "s3://${BUCKET}/data/stats.json" data/stats.json \
    --endpoint-url "$R2_ENDPOINT" --quiet 2>/dev/null || echo '{"total_jobs":0,"total_companies":0,"jobs_this_week":0}' > data/stats.json
aws s3 cp "s3://${BUCKET}/data/categories.json" data/categories.json \
    --endpoint-url "$R2_ENDPOINT" --quiet 2>/dev/null || echo '[]' > data/categories.json

JOB_COUNT=$(find content/jobs -name '*.md' -not -name '_index.md' 2>/dev/null | wc -l)
echo "Sync complete. ${JOB_COUNT} job files ready for Hugo build."
