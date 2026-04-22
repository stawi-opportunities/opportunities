# Runbook — Rebuild Valkey KV from R2

**Symptom:** Dedup lookups all miss; cluster explosion in logs; duplicate canonicals in search.

**Cause:** Valkey replica loss, zone outage, or a bad FLUSHDB.

**Recovery (5–15 min):**

1. Confirm Valkey reachable: redis-cli -u $VALKEY_URL PING → PONG
2. curl -XPOST $WORKER_URL/_admin/kv/rebuild
3. Watch response for counters: {"rows":12345,"cluster_keys_set":12345,"files_scanned":48}
4. Verify: redis-cli -u $VALKEY_URL GET cluster:<any_cluster_id> returns a JSON snapshot.

**Scope note:** The rebuild repopulates cluster:{cluster_id} only. dedup:{hard_key} keys rebuild lazily as variants flow through; compaction re-merges any duplicate clusters by hard_key overlap.

**Success signal:** Writer logs stop emitting "dedup:miss" at high rate; canonical upsert rate returns to normal within 10 min.
