# Migrate opportunities domain models to `data.BaseModel`

**Status:** Tracked tech debt, not yet scheduled.

## Why

The golang-patterns skill enforces a mandatory rule: every model embeds `github.com/pitabwire/frame/data.BaseModel`. It gives us a consistent ID format (`varchar(50)` XIDs), automatic `TenantID` / `PartitionID` columns for multi-tenant safety, and unified `CreatedAt` / `ModifiedAt` / `DeletedAt` columns handled by Frame's repository layer.

opportunities predates that rule. Every model under `pkg/domain/` uses plain GORM with `int64 autoIncrement` IDs:

| File | Models with int64 IDs |
|---|---|
| `pkg/domain/candidate.go` | `CandidateProfile`, `MatchRecord`, `RerankCache` |
| `pkg/domain/models.go` | `Source`, `CrawlJob`, `RawPayload`, `JobVariant`, `JobCluster`, `JobClusterMember`, `CanonicalJob`, `CrawlPageState`, `RejectedJob`, `SavedJob` |
| `pkg/domain/snapshot.go` | (only wire contracts, no DB) |

That's 13 models to convert — and because IDs change type, every repository, handler, and API-surface integer ID propagates through the change.

## Impact map

| Layer | What changes |
|---|---|
| **Schema** | Drop `id bigserial PRIMARY KEY`, add `id varchar(50) PRIMARY KEY`. Every FK column (`source_id bigint`, `canonical_job_id bigint`, `cluster_id bigint`, etc.) migrates to `varchar(50)`. Backfill existing rows: generate XIDs for each PK, rewrite FKs. Requires a maintenance window because the change isn't reversible and the crawler can't run during the FK rewrite. |
| **Repositories** | `GetByID(ctx, id int64)` becomes `GetByID(ctx, id string)`. Every `WHERE id = ?` clause stays the same, but the caller's type changes. ~50 call sites across 10 repository files. |
| **Handlers** | Every `strconv.ParseInt(idStr, 10, 64)` → just take the path param as a string (XIDs are URL-safe). ~30 endpoints across the three apps. |
| **Events** | Payloads like `VariantPayload{VariantID int64}` grow string variants. In-flight NATS messages are typed, so the switch is a one-shot cutover, not a gradual migration. |
| **UI contract** | Frontend currently uses `job.id: number` in TypeScript types. `id` becomes `string`. Affects `JobSnapshot`, `SearchResult`, saved-jobs responses, match records. |
| **R2 snapshots** | Slugs don't change (`jobs/{slug}.json`), so R2 is unaffected. |

## Proposed sequencing (5 PRs)

**PR 1 — Foundation.** Add `data.BaseModel` alongside existing `int64 ID`. Models temporarily have both fields. New rows get XIDs; old rows keep their int64s. Repositories grow dual lookups (`GetByID` by int64, `GetByXID` by string). This PR is strictly additive and backward-compatible — no running code changes behaviour.

**PR 2 — Backfill.** Migration runs: `UPDATE ... SET xid = gen_random_uuid()` on every row (with `xid_generator()` function for real XIDs). No read path changes yet; everything still keyed on int64.

**PR 3 — Flip read path.** Repositories, handlers, events, UI types switch to XIDs. int64 IDs still populated but unused. This is the big PR (~100 files) and the one that breaks the build everywhere simultaneously — unavoidable with typed APIs.

**PR 4 — Drop int64.** Migration drops `id bigserial` and all `*_id bigint` columns. Models remove the int64 fields. Clean single-source-of-truth.

**PR 5 — Tenancy.** Populate `TenantID` and `PartitionID` on every model. Until this, they're null on every row. Once they're real, we turn on `FunctionAccessInterceptor` at the gateway.

## Non-goals

- **Backwards-compatible public IDs.** External links (share URLs, CF Pages caches) already use `slug`, not int64 ID. `slug` is unaffected.
- **Multi-tenant isolation now.** PR 5 is the only place tenancy actually goes live.
- **Renaming JSON fields.** The wire contract between API and UI keeps `id: string` once flipped — no `id_v2` dance.

## Effort estimate

| PR | Effort |
|---|---|
| PR 1 Foundation | 1 day |
| PR 2 Backfill | 0.5 day (mostly waiting on migration) |
| PR 3 Flip read path | 3-4 days (touches every app + UI) |
| PR 4 Drop int64 | 0.5 day |
| PR 5 Tenancy | 2 days (needs coordination with Thesa for tenant IDs) |
| **Total** | **~8 days of focused work** |

## Risks

- **Downtime during PR 3 rollout.** The events stream needs to drain before the crawler rolls the new binary, or in-flight messages get dropped. Either pause seeding for an hour, or version the payloads so both can co-exist during the rollout (extra complexity).
- **UI cache poisoning.** Browsers with stale bundles will send integer IDs to an API expecting strings. Mitigate with a short-lived compatibility shim on the API (accept both, prefer string).
- **Schema migration failures mid-way.** Before PR 3, take a full `pg_dump` snapshot; rollback is restore-from-snapshot because the FK rewrite is not reversible on live data.

## What to do in the meantime

- **New models MUST use BaseModel.** Any new domain type added after this spec must embed `data.BaseModel` from day one — even if it means living alongside the int64 majority. This prevents the debt growing.
- **Code review gate.** Reject PRs adding `ID int64 autoIncrement` to a domain struct. Link to this spec.
- **Linting.** Consider a custom `go vet`-style check or a CI grep for `gorm:"primaryKey;autoIncrement"` that fails on new occurrences.

## Current session's exemption

In this session (commit `3765b7a`), new CV strength fields were added to `CandidateProfile` (`CVScore`, `CVReportJSON`, etc.) as additional columns on the existing BaseModel-less struct, not as a new model. That's correct — we don't split off a new `CVReport` table to avoid creating a mixed-style surface. When PR 3 of this migration lands, `CandidateProfile` gets the BaseModel conversion in one pass.
