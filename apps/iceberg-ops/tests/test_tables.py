def test_current_merge_targets_unique():
    """Each _current table appears at most once as a MERGE target."""
    from stawi_iceberg_ops.tables import CURRENT_TABLE_MERGES

    targets = [tgt for _, tgt, _ in CURRENT_TABLE_MERGES]
    assert len(targets) == len(set(targets)), "Duplicate merge targets: " + str(targets)


def test_hot_tables_subset_of_all():
    """Every HOT_TABLE must also be listed in ALL_TABLES."""
    from stawi_iceberg_ops.tables import ALL_TABLES, HOT_TABLES

    for t in HOT_TABLES:
        assert t in ALL_TABLES, f"{t!r} is in HOT_TABLES but missing from ALL_TABLES"


def test_all_tables_count():
    """Sanity-check the full table list has the expected 20 entries."""
    from stawi_iceberg_ops.tables import ALL_TABLES

    assert len(ALL_TABLES) == 20, f"Expected 20 tables, got {len(ALL_TABLES)}"


def test_merge_sources_exist_in_all_tables():
    """Every MERGE source table must be in ALL_TABLES."""
    from stawi_iceberg_ops.tables import ALL_TABLES, CURRENT_TABLE_MERGES

    for src, _, _ in CURRENT_TABLE_MERGES:
        assert src in ALL_TABLES, f"MERGE source {src!r} not in ALL_TABLES"
