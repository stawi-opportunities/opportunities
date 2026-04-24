#!/usr/bin/env bash
# Applies idx_jobs_rt DDL to the running Manticore StatefulSet.
# The DDL is idempotent — Manticore returns "table already exists" on re-run,
# which the materializer's searchindex.Apply also handles gracefully.
#
# Usage:
#   ./scripts/bootstrap/create-manticore-schema.sh [path/to/ddl.sql]
#
# Default DDL file: definitions/manticore/idx_jobs_rt.sql
set -euo pipefail

REPO_ROOT=$(git -C "$(dirname "$(readlink -f "$0")")" rev-parse --show-toplevel)
DDL_FILE=${1:-"${REPO_ROOT}/definitions/manticore/idx_jobs_rt.sql"}

if [[ ! -f "${DDL_FILE}" ]]; then
    echo "ERROR: DDL file not found: ${DDL_FILE}"
    exit 1
fi

echo "Applying Manticore schema from ${DDL_FILE} ..."
kubectl -n stawi-jobs exec -i svc/manticore -- \
    /usr/bin/mysql -h127.0.0.1 -P9306 < "${DDL_FILE}"

echo "Verifying table exists..."
kubectl -n stawi-jobs exec -i svc/manticore -- \
    /usr/bin/mysql -h127.0.0.1 -P9306 -e "SHOW TABLES LIKE 'idx_jobs_rt';" \
    | grep -q idx_jobs_rt && echo "idx_jobs_rt confirmed." || {
        echo "ERROR: idx_jobs_rt not found after applying DDL"
        exit 1
    }
