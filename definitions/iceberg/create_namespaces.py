#!/usr/bin/env python3
"""
DEPRECATED — kept as historical reference only.

Namespace creation is now handled by the `bootstrap-iceberg` Go
subcommand of the writer image, executed by the
`opportunities-iceberg-bootstrap` Kubernetes Job on every FluxCD
reconcile. See pkg/icebergclient/schemas.go for the canonical list.

Idempotent: create opportunities + candidates namespaces. Safe to re-run.
"""
import os
import sys
from pyiceberg.catalog import load_catalog


def main() -> None:
    cat = load_catalog("stawi", **{
        "type": "sql",
        "uri": os.environ["ICEBERG_CATALOG_URI"],
        "s3.endpoint": os.environ.get("R2_ENDPOINT", ""),
        "s3.access-key-id": os.environ["R2_ACCESS_KEY_ID"],
        "s3.secret-access-key": os.environ["R2_SECRET_ACCESS_KEY"],
        "s3.region": os.environ.get("R2_REGION", "auto"),
        "warehouse": f"s3://{os.environ['R2_CHRONICLE_BUCKET']}/iceberg",
    })
    for ns in (("opportunities",), ("candidates",)):
        try:
            cat.create_namespace(ns)
            print(f"created namespace {ns}")
        except Exception as e:
            if "already exists" in str(e).lower():
                print(f"namespace {ns} exists — skipping")
            else:
                raise


if __name__ == "__main__":
    main()
