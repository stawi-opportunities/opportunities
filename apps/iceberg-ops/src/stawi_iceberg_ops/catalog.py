"""Load the stawi Iceberg catalog. Mirrors configuration used in Wave 1 create scripts."""
import os

from pyiceberg.catalog import load_catalog as pyiceberg_load
from pyiceberg.catalog.sql import SqlCatalog


def load_catalog() -> SqlCatalog:
    return pyiceberg_load("stawi", **{
        "type": "sql",
        "uri": os.environ["ICEBERG_CATALOG_URI"],
        "s3.endpoint": os.environ.get("R2_ENDPOINT", ""),
        "s3.access-key-id": os.environ["R2_ACCESS_KEY_ID"],
        "s3.secret-access-key": os.environ["R2_SECRET_ACCESS_KEY"],
        "s3.region": os.environ.get("R2_REGION", "auto"),
        "s3.path-style-access": "true",
        "warehouse": f"s3://{os.environ['R2_LOG_BUCKET']}/iceberg",
    })
