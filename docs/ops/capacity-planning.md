# Capacity Planning

Per-service sizing recommendations at four operational scales.

All stawi.jobs services adapt their in-memory working set to the pod's actual
cgroup memory limit at runtime via `pkg/memconfig`. You can raise or lower
`limits.memory` without restarting — the service resizes batch sizes and
parallelism within 30 s.

**Rule of thumb:** `requests` = minimum for reliable startup; `limits` = advisory
ceiling the service will not exceed under any load.

---

## Sizing table

| Service | Metric | Prototype (10k jobs) | Current-target (1M) | Growth-target (100M) | Billion-scale (1B+) |
|---------|--------|---------------------|--------------------|--------------------|-------------------|
| **api** | CPU req/lim | 100m / 500m | 200m / 2000m | 500m / 4000m | 1000m / 8000m |
| | Memory req/lim | 128Mi / 512Mi | 256Mi / 2Gi | 512Mi / 4Gi | 1Gi / 8Gi |
| | Replicas | 1 | 3–12 (HPA) | 6–30 (HPA) | 20–100 (HPA) |
| **writer** | CPU req/lim | 250m / 1000m | 500m / 4000m | 1000m / 8000m | 2000m / 16000m |
| | Memory req/lim | 512Mi / 2Gi | 1Gi / 12Gi | 2Gi / 24Gi | 4Gi / 48Gi |
| | Replicas | 1 | 2–6 (HPA) | 4–12 (HPA) | 8–24 (HPA) |
| **materializer** | CPU req/lim | 100m / 500m | 250m / 2000m | 500m / 4000m | 1000m / 8000m |
| | Memory req/lim | 128Mi / 512Mi | 256Mi / 2Gi | 512Mi / 4Gi | 1Gi / 8Gi |
| | Replicas | 1 | 2–4 (HPA) | 4–12 (HPA) | 8–24 (HPA) |
| **crawler** | CPU req/lim | 100m / 500m | 200m / 2000m | 500m / 4000m | 1000m / 8000m |
| | Memory req/lim | 256Mi / 1Gi | 512Mi / 4Gi | 1Gi / 8Gi | 2Gi / 16Gi |
| | Replicas | 1 | 2–6 (HPA) | 4–16 (HPA) | 8–32 (HPA) |
| **worker** | CPU req/lim | 100m / 500m | 250m / 2000m | 500m / 4000m | 1000m / 8000m |
| | Memory req/lim | 256Mi / 1Gi | 512Mi / 4Gi | 1Gi / 8Gi | 2Gi / 16Gi |
| | Replicas | 1 | 2–4 (HPA) | 4–12 (HPA) | 8–24 (HPA) |
| **candidates** | CPU req/lim | 100m / 500m | 200m / 2000m | 500m / 4000m | 1000m / 8000m |
| | Memory req/lim | 128Mi / 512Mi | 256Mi / 2Gi | 512Mi / 4Gi | 1Gi / 8Gi |
| | Replicas | 1 | 2–4 (HPA) | 4–12 (HPA) | 8–24 (HPA) |

---

## External service notes

### Manticore Search

Manticore is a shared StatefulSet (`manticore.stawi-jobs.svc`). It is the primary
hot-path bottleneck under high concurrent search load.

| Scale | Recommended sizing | Notes |
|-------|--------------------|-------|
| Prototype (10k) | 1 replica, 512Mi / 2Gi, 1 CPU | Single RT index, no KNN queries |
| Current-target (1M) | 1 replica, 2Gi / 8Gi, 2 CPU | KNN queries on 1536-dim vectors are expensive |
| Growth-target (100M) | 2 replicas (percolate + query split), 8Gi / 32Gi, 4 CPU | Shard idx_jobs_rt by country or category |
| Billion-scale | Dedicated Manticore cluster, 16Gi+ RAM per node | Requires distributed index; evaluate Elasticsearch migration |

**Key driver:** The KNN `embedding` column (1536 float32 × 4 bytes = 6 KiB per row).
At 1M rows the in-memory HNSW index is ~6 GiB; at 100M rows it is ~600 GiB.
Plan for a dedicated high-memory node at growth-target scale.

**Write throughput:** Materializer upserts are single-doc REPLACE operations.
At 500 upserts/s the Manticore RT merge overhead becomes significant — consider
batching via `INSERT ... BULK INSERT` if materializer upsert lag exceeds 60 s.

### Valkey

Valkey stores dedup keys (`dedup:{hard_key}`) and cluster snapshots
(`cluster:{cluster_id}`). All values are small JSON blobs (< 4 KiB).

| Scale | Recommended sizing | Notes |
|-------|--------------------|-------|
| Prototype (10k) | 1 replica, 128Mi / 256Mi | In-memory only; use persistence snapshots |
| Current-target (1M) | 1 replica, 512Mi / 2Gi | ~2 B per dedup key × 1M = 2 GiB RAM |
| Growth-target (100M) | 2-shard cluster, 4Gi / 16Gi each | Hash-slot sharding on cluster_id prefix |
| Billion-scale | Dedicated Valkey cluster (6+ nodes), 32Gi+ | Evaluate cluster-mode with automatic resharding |

**Key driver:** Dedup key count ≈ unique hard_keys ≈ 3–5× canonical job count.
At 100M canonicals expect 300–500M dedup keys; at ~64 bytes each that is 20–32 GiB.

**Persistence:** Enable `appendonly yes` to survive pod restarts without a full
KV rebuild. The rebuild runbook (`docs/ops/disaster-recovery-runbook.md`) documents
the recovery path if Valkey is lost.

---

## HPA configuration notes

All six services have `autoscaling.enabled: true` in their HelmReleases.
HPA triggers are:
- CPU utilization target: 70%
- Memory utilization target: 85% (writer only)

For the writer at growth-target scale, add a custom NATS JetStream metric
(`nats_jetstream_consumer_num_pending`) as a secondary HPA trigger to scale
proactively with backlog depth rather than waiting for CPU to spike.

---

## memconfig adaptive sizing

`pkg/memconfig` re-reads the cgroup memory limit every 30 s and adjusts:

| Service | Subsystem | Budget fraction |
|---------|-----------|----------------|
| writer | writer-buffer | 30% of limit |
| writer | compact parallelism | 50% of limit |
| worker | kv-rebuild map | 30% of limit |
| candidates | stale-reader heap | 20% of limit |
| materializer | Manticore bulk batch | 10% of limit |

Raising `limits.memory` automatically increases throughput without code changes.
Lowering it automatically reduces throughput — the service works more slowly
but never OOMs.
