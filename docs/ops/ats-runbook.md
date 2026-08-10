# Employer ATS runbook

Service: `apps/ats` (Frame, **Postgres only**) · UI: `ui/ats`  
Layout follows **service-profile**: `cmd` / `config` / `migrations` / `service/{models,repository,business,handlers}` / `tests`.

Design: `docs/superpowers/specs/2026-08-10-employer-ats-design.md`

## Architecture (golang-patterns)

| Layer | Package |
|-------|---------|
| Interaction | `service/handlers` (**Connect RPC** `ats.v1.AtsService`; thin) |
| Business | `service/business` |
| Repository | `service/repository` (`datastore.BaseRepository`) |
| Models | `service/models` (`data.BaseModel`) |

**Setup vs runtime**

- Setup Job: `frame.ShouldRunSetup` → `repository.Migrate` only → exit  
- Runtime: `DATABASE_URL` Postgres pool required; **no** migrate, **no** sqlite  

**Tests:** `apps/ats/tests` — `frametests.FrameBaseTestSuite` + `testpostgres` (Docker).

## Local development

```bash
# Postgres (repo compose or any PG)
export DATABASE_URL=postgres://postgres:postgres@127.0.0.1:5432/ats?sslmode=disable

# One-shot migrate (setup process)
DO_SETUP=true DATABASE_URL=$DATABASE_URL go run ./apps/ats/cmd

# Runtime (dev tenancy headers — no SeedDemo)
AUTH_REQUIRE_JWT=false DATABASE_URL=$DATABASE_URL go run ./apps/ats/cmd

# SPA (dev headers when JWT off)
cd ui/ats && VITE_ATS_DEV_HEADERS=true npm run dev
```

Or with existing monorepo infra:

```bash
make infra-up
# create DB/user as needed, then DO_SETUP + run as above
```

Dev auth headers (SPA, only with `VITE_ATS_DEV_HEADERS=true`): `X-Profile-ID`, `X-Tenant-ID`, `X-Partition-ID`.  
Production SPA: `VITE_OIDC_*` + `@stawi/auth-runtime` Bearer via `runtime.fetch`.

### Optional peer env (runtime)

| Env | Purpose |
|-----|---------|
| `NOTIFICATION_SERVICE_URI` | Interview email/ICS delivery |
| `MESSAGE_TEMPLATE_ATS_INTERVIEW_SCHEDULED` | Notify template name |
| `PUBLIC_SITE_URL` | Deep links in invites |
| `ATS_MATCHING_DATABASE_URL` | Optional `candidate_profiles` read DB |
| `ATS_PRODUCT_DATABASE_URL` | Optional dual-write to product opportunities |
| `ATS_OUTBOX_POLL_SECONDS` | Outbox drain interval (default 15) |
| `CALENDAR_SERVICE_URI` | Optional `service_calendar` for interview slots/bookings |
| `CALENDAR_SERVICE_DIRECT` | `true` for local HTTP without OAuth mesh |

There is **no** SeedDemo RPC or auto-seed. Create jobs and candidates through the real API.

## Makefile

```bash
make run-ats          # requires DATABASE_URL; AUTH_REQUIRE_JWT=false
go test ./apps/ats/... -count=1   # testcontainers Postgres
```

## Production

1. Cloud Run **setup Job**: `argv=["setup"]` / `DO_SETUP=true`  
   - Migrates schema  
   - Registers **`service_ats`** permission namespace from proto (`frame.WithPermissionRegistration`)  
2. Runtime: Postgres only, `AUTH_REQUIRE_JWT=true`, OIDC  
   - Connect interceptors: JWT + claims + **FunctionAccess** (`ATS_ENFORCE_PERMISSIONS` default on)  
   - **ProjectionPublisher** (ATS board + optional product dual-write)  
   - **SQLMatchingTalent** (or empty when tables absent)  
   - **LedgerBillingEmitter** (`result_hire_{job}_{app}`)  
   - **NotificationNotifier** + **OutboxWorker** drain  
   - **Idempotency-Key** on side-effecting RPCs  
3. Deploy: SPA audience path for product API; SA name `service-ats` ↔ namespace `service_ats`  
4. Grant partition roles (OWNER/ADMIN/OPERATOR/MEMBER/VIEWER) so Keto `granted_*` tuples resolve  

### Permission map (summary)

| Permission | Roles (typical) |
|------------|-----------------|
| `ats_dashboard_view` | owner, admin, operator, member, viewer |
| `ats_job_manage` / `ats_publish` | owner, admin, operator |
| `ats_application_manage` | owner, admin, operator, member |
| `ats_hire` | owner, admin, operator |
| `ats_interview_manage` | owner, admin, operator, member |
| `ats_ai_use` | owner, admin, operator, member |

Declared in `apps/ats/proto/ats/v1/ats.proto` via `common.v1.service_permissions` + `method_permissions`.

## API (Connect)

Proto: `apps/ats/proto/ats/v1/ats.proto`  
Generated: `apps/ats/gen/ats/v1` (+ `atsv1connect`)  
Regenerate: `cd apps/ats/proto && buf generate`

Procedures (JSON Connect):

```
POST /ats.v1.AtsService/GetDashboard
POST /ats.v1.AtsService/ListJobs
POST /ats.v1.AtsService/CreateJob
…
```

SPA uses `Connect-Protocol-Version: 1` + JSON body. Agents use generated `atsv1connect.NewAtsServiceClient`.

Publish to `buf.build` when ready for Flutter/TS generated clients across products.

## Deploy

See [ats-deploy-checklist.md](ats-deploy-checklist.md) for SA, setup Job, gateway, and smoke steps.

Local Postgres + ATS containers (optional):

```bash
make infra-up
docker compose -f deploy/docker-compose.yml --profile ats run --rm ats-setup
docker compose -f deploy/docker-compose.yml --profile ats up ats
```
