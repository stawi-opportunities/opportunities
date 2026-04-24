# Schema Evolution — Iceberg Event Payloads

**Audience:** Developers adding, changing, or removing fields on any Iceberg-backed event type.

## Files to update for any schema change

| File | Purpose |
|------|---------|
| `pkg/events/v1/<domain>.go` | Go struct with `parquet:"..."` tags |
| `definitions/iceberg/_schemas.py` | Iceberg `NestedField` declarations (used by create_tables.py) |
| `pkg/icebergclient/arrow_schemas.go` | Arrow `schema.Field` declarations (used by writer Parquet flush) |
| `pkg/icebergclient/arrow_build.go` | Arrow record builder assignments |
| Tests in `pkg/events/v1/*_test.go` | Round-trip envelope encode/decode |

Missing any one of these four causes a schema mismatch that will produce silent data loss or a panic in the writer.

---

## Case 1 — Add an OPTIONAL field (non-breaking)

This is the most common case. New consumers can read it immediately; old consumers (and existing Parquet files) simply have `null` for rows written before the backfill.

### Steps

1. **Go struct** (`pkg/events/v1/<domain>.go`): add the field with `parquet:"field_name,optional"`.

2. **`_schemas.py`**: add `_opt(next_id, "field_name", StringType())` (or appropriate type). Assign the next available field ID for that table — **never reuse an ID**.

3. **`arrow_schemas.go`**: add `arrow.Field{Name: "field_name", Type: arrow.BinaryTypes.String, Nullable: true}` to the appropriate `Schema(...)` call.

4. **`arrow_build.go`**: add a null-safe assignment in the record builder (use the `appendStringOpt` / `appendFloat64Opt` helpers for optional fields).

5. **ALTER the live Iceberg table** — run the ad-hoc pyiceberg script below (or use `scripts/bootstrap/alter-iceberg-schema.py`):

   ```python
   # scripts/bootstrap/alter-iceberg-schema.py (see template at end of this doc)
   table.update_schema().add_column("field_name", StringType(), required=False).commit()
   ```

   The `add_column` call is safe to run against a live table — Iceberg applies it as a metadata-only change. Old Parquet files continue to read as `null` for the new column.

6. **Tests**: update `pkg/events/v1/envelope_test.go` round-trip for the affected struct.

### Worked example — adding `industry_v2` to `CanonicalUpsertedV1`

**`pkg/events/v1/canonicals.go`**:
```go
// existing field after Category:
Category     string `json:"category"     parquet:"category,optional"`
// add:
IndustryV2   string `json:"industry_v2"  parquet:"industry_v2,optional"`
```

**`definitions/iceberg/_schemas.py`** (inside `CANONICALS_UPSERTED = Schema(...)`):
```python
# existing: _opt(22, "category", StringType()),
_opt(23, "industry_v2", StringType()),   # <-- new; use next available ID
```

**`pkg/icebergclient/arrow_schemas.go`** (CanonicalsUpsertedSchema):
```go
// existing:
{Name: "category", Type: arrow.BinaryTypes.String, Nullable: true},
// add:
{Name: "industry_v2", Type: arrow.BinaryTypes.String, Nullable: true},
```

**`pkg/icebergclient/arrow_build.go`** (buildCanonicalsUpserted):
```go
appendStringOpt(b, ev.IndustryV2)
```

**ALTER TABLE** (run once per environment):
```bash
python scripts/bootstrap/alter-iceberg-schema.py \
    --table jobs.canonicals_upserted \
    --add-column industry_v2 string optional
```

---

## Case 2 — Add a REQUIRED field (breaking)

Required fields cannot have `null` in existing Parquet files. This is a **planned rollout** operation.

### Two strategies

**Strategy A — Cold table (no active writers during migration window)**

1. Stop all writers consuming that topic (scale to 0 or drain the NATS consumer).
2. Create a new Iceberg table with the updated schema (rename old table, create fresh).
3. Backfill old data into the new table with a default value.
4. Switch writers and readers to the new table.
5. Drop the old table after 7 days.

**Strategy B — Hot table (default-value backfill)**

1. Add the field as **optional** first (follow Case 1 procedure).
2. Run a backfill job that reads existing rows and writes the default value.
3. Once all rows have a non-null value, issue an `ALTER TABLE ... alter_column(required=True)`.
4. Update the schema files to `required=True`.

Both strategies require a code-freeze window to synchronize schema version bumps.

---

## Case 3 — Remove a field (soft-delete pattern)

Never immediately drop. Removing a field that readers still query causes read panics.

1. **Stop emitting** the field in the Go struct — set it to zero/empty on write, but keep it in the struct.
2. **Wait for all consumers** to deploy a version that no longer reads the field.
3. **Soft-remove from Go struct**: remove the field from the struct and `arrow_build.go` assignment.
4. **Keep in `_schemas.py` and `arrow_schemas.go`** for one release cycle to allow old Parquet files to remain readable.
5. **ALTER TABLE drop column** after confirming no consumer reads it:

   ```python
   table.update_schema().delete_column("field_name").commit()
   ```

6. Remove from `_schemas.py`, `arrow_schemas.go`, and struct in a final cleanup PR.

---

## Case 4 — Change field type

Iceberg supports **compatible widening** without data migration:

| From | To | Safe? |
|------|----|-------|
| `int` | `long` | Yes |
| `float` | `double` | Yes |
| `decimal(p,s)` | `decimal(p2,s)` where p2 > p | Yes |
| Any other change | Any | **No** — requires table replace |

For incompatible changes, use the table-replace path from Case 2 Strategy A.

To widen a type:
```python
table.update_schema().update_column_type("field_name", LongType()).commit()
```

---

## `scripts/bootstrap/alter-iceberg-schema.py` template

See the file at `scripts/bootstrap/alter-iceberg-schema.py`.
It demonstrates the pyiceberg ALTER TABLE pattern for ad-hoc schema changes.
