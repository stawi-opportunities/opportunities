# Runbook — Rebuild Manticore from zero

**Symptom:** idx_opportunities_rt corrupt, empty, or stale. Serving returns 503 or wrong results.

**Cause:** Manticore restart without persistent volume, or admin wiped the index.

**Recovery (60–120 min):**

1. Confirm R2 intact: aws s3 ls s3://opportunities-log/canonicals_current/ | wc -l — expect ≥200.
2. curl -XPOST $MANTICORE_URL/sql?mode=raw -d "query=DROP TABLE IF EXISTS idx_opportunities_rt"
3. Re-provision the schema: kubectl apply -f definitions/manticore/idx_opportunities_rt.sql (or re-run Phase 2 migration).
4. Reset materializer watermarks: redis-cli SET mat:watermark:canonicals "" (and other partitions).
5. kubectl rollout restart deploy/materializer
6. kubectl logs -f deploy/materializer | grep "manticore upsert" — expect ≥500 upserts/min for the first hour.
7. Check row count every 10 min: curl -s $MANTICORE_URL/sql?mode=raw -d "query=SELECT COUNT(*) FROM idx_opportunities_rt"
8. Lift 503 when count exceeds launch threshold (50k).

**Success signal:** /api/v2/search?q=engineer returns ≥10 hits.
