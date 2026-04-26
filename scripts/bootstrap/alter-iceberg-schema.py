#!/usr/bin/env python3
"""
alter-iceberg-schema.py — Ad-hoc Iceberg ALTER TABLE template.

Demonstrates the pyiceberg pattern for adding, widening, or deleting columns
on a live Iceberg table without stopping the writers.

See docs/ops/schema-evolution.md for the full decision tree on when to use
each operation.

Usage:
    pip install pyiceberg[sql-postgres,s3fs]
    python scripts/bootstrap/alter-iceberg-schema.py

Environment variables (same as create_namespaces.py / create_tables.py):
    ICEBERG_CATALOG_URI     postgres://user:pass@host:5432/stawi_jobs?sslmode=require
    R2_ACCESS_KEY_ID
    R2_SECRET_ACCESS_KEY
    R2_LOG_BUCKET           opportunities-log
    R2_ENDPOINT             https://<account_id>.r2.cloudflarestorage.com
    R2_REGION               auto  (default)

Customise the ALTER_OPS list below before running.
"""

import os
import sys
from pyiceberg.catalog import load_catalog
from pyiceberg.types import (
    BooleanType,
    DoubleType,
    FloatType,
    IntegerType,
    LongType,
    StringType,
    TimestamptzType,
)


def get_catalog():
    return load_catalog("stawi", **{
        "type": "sql",
        "uri": os.environ["ICEBERG_CATALOG_URI"],
        "s3.endpoint": os.environ.get("R2_ENDPOINT", ""),
        "s3.access-key-id": os.environ["R2_ACCESS_KEY_ID"],
        "s3.secret-access-key": os.environ["R2_SECRET_ACCESS_KEY"],
        "s3.region": os.environ.get("R2_REGION", "auto"),
        "warehouse": f"s3://{os.environ['R2_LOG_BUCKET']}/iceberg",
    })


# ---------------------------------------------------------------------------
# Define the operations to apply.
# Comment out operations that don't apply to your change.
# ---------------------------------------------------------------------------

# Example: add optional string column to jobs.canonicals_upserted
# (the worked example from docs/ops/schema-evolution.md)
ALTER_OPS = [
    {
        "table": ("opportunities", "canonicals_upserted"),
        "op": "add_optional",
        "column": "industry_v2",
        "type": StringType(),
    },

    # Example: widen int → long (compatible, no data migration)
    # {
    #     "table": ("opportunities", "variants"),
    #     "op": "widen_type",
    #     "column": "salary_min",
    #     "type": LongType(),
    # },

    # Example: soft-delete — remove column that all consumers have stopped reading
    # {
    #     "table": ("opportunities", "variants"),
    #     "op": "delete_column",
    #     "column": "legacy_field",
    # },
]


def apply_op(catalog, op: dict) -> None:
    ns, tbl = op["table"]
    table = catalog.load_table((ns, tbl))
    updater = table.update_schema()

    if op["op"] == "add_optional":
        print(f"  ADD COLUMN {op['column']} ({op['type']}, optional) to {ns}.{tbl}")
        updater.add_column(op["column"], op["type"], required=False)

    elif op["op"] == "add_required":
        # WARNING: only valid if all existing rows already have non-null values
        # (e.g., after a backfill). See docs/ops/schema-evolution.md Case 2.
        print(f"  ADD COLUMN {op['column']} ({op['type']}, required) to {ns}.{tbl}")
        updater.add_column(op["column"], op["type"], required=True)

    elif op["op"] == "widen_type":
        print(f"  UPDATE TYPE {op['column']} -> {op['type']} on {ns}.{tbl}")
        updater.update_column(doc=op["column"], field_type=op["type"])

    elif op["op"] == "delete_column":
        print(f"  DELETE COLUMN {op['column']} from {ns}.{tbl}")
        updater.delete_column(op["column"])

    else:
        raise ValueError(f"Unknown op: {op['op']!r}")

    updater.commit()
    print(f"  committed.")


def main() -> None:
    required = ["ICEBERG_CATALOG_URI", "R2_ACCESS_KEY_ID", "R2_SECRET_ACCESS_KEY", "R2_LOG_BUCKET"]
    missing = [v for v in required if not os.environ.get(v)]
    if missing:
        print(f"ERROR: missing env vars: {', '.join(missing)}", file=sys.stderr)
        sys.exit(1)

    catalog = get_catalog()

    for op in ALTER_OPS:
        ns, tbl = op["table"]
        print(f"Applying op '{op['op']}' on {ns}.{tbl} ...")
        apply_op(catalog, op)

    print("All schema alterations complete.")


if __name__ == "__main__":
    main()
