#!/usr/bin/env bash
# Creates Iceberg namespaces and tables by running a one-shot Kubernetes Job.
# The job clones the repo and runs definitions/iceberg/create_namespaces.py
# and definitions/iceberg/create_tables.py against the live catalog.
#
# Prerequisites:
#   - Vault secrets seeded (run seed-vault.sh first)
#   - ExternalSecrets synced: iceberg-catalog-credentials-stawi-jobs and
#     r2-log-credentials-stawi-jobs must exist in the stawi-jobs namespace
#   - Outbound internet access from the cluster (for git clone + pip install)
#
# Usage:
#   ./scripts/bootstrap/create-iceberg.sh
set -euo pipefail

JOB_NAME="iceberg-bootstrap-$(date +%s)"

echo "Submitting Iceberg bootstrap Job: ${JOB_NAME} ..."

kubectl apply -f - <<EOF
apiVersion: batch/v1
kind: Job
metadata:
  namespace: stawi-jobs
  name: ${JOB_NAME}
spec:
  backoffLimit: 0
  ttlSecondsAfterFinished: 3600
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: bootstrap
          image: python:3.12-slim
          command:
            - bash
            - -c
            - |
              set -euo pipefail
              apt-get update -qq && apt-get install -y -qq git
              git clone --depth 1 https://github.com/antinvestor/stawi-jobs /tmp/sj
              cd /tmp/sj/definitions/iceberg
              pip install -q -r requirements.txt
              echo "Creating namespaces..."
              python create_namespaces.py
              echo "Creating tables..."
              python create_tables.py
              echo "Iceberg bootstrap complete."
          env:
            - name: ICEBERG_CATALOG_URI
              valueFrom:
                secretKeyRef:
                  name: iceberg-catalog-credentials-stawi-jobs
                  key: ICEBERG_CATALOG_URI
            - name: R2_ACCESS_KEY_ID
              valueFrom:
                secretKeyRef:
                  name: r2-log-credentials-stawi-jobs
                  key: R2_LOG_ACCESS_KEY_ID
            - name: R2_SECRET_ACCESS_KEY
              valueFrom:
                secretKeyRef:
                  name: r2-log-credentials-stawi-jobs
                  key: R2_LOG_SECRET_ACCESS_KEY
            - name: R2_LOG_BUCKET
              value: "stawi-jobs-log"
            - name: R2_ENDPOINT
              valueFrom:
                secretKeyRef:
                  name: r2-log-credentials-stawi-jobs
                  key: R2_LOG_ENDPOINT
EOF

echo "Waiting for Job ${JOB_NAME} to complete (timeout 10m)..."
kubectl wait --for=condition=complete --timeout=600s "job/${JOB_NAME}" -n stawi-jobs \
    || { echo "Job failed — check logs:"; kubectl logs -n stawi-jobs "job/${JOB_NAME}"; exit 1; }

echo "Iceberg namespaces and tables bootstrapped successfully."
