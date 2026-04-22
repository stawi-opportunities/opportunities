import os
import pytest

# The sql-postgres extra (sqlalchemy) is required to import catalog.py.
# Skip the whole module when those extras are not installed in the test env.
sqlalchemy = pytest.importorskip("sqlalchemy", reason="pyiceberg[sql-postgres] not installed")


def test_load_catalog_env_required(monkeypatch):
    """load_catalog() must raise KeyError when ICEBERG_CATALOG_URI is absent."""
    monkeypatch.delenv("ICEBERG_CATALOG_URI", raising=False)
    # Also clear R2 vars so we don't accidentally pass with stale env.
    monkeypatch.delenv("R2_ACCESS_KEY_ID", raising=False)
    monkeypatch.delenv("R2_SECRET_ACCESS_KEY", raising=False)
    monkeypatch.delenv("R2_LOG_BUCKET", raising=False)

    from stawi_iceberg_ops.catalog import load_catalog

    with pytest.raises(KeyError):
        load_catalog()
