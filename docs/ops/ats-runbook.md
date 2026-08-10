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

# Runtime
AUTH_REQUIRE_JWT=false DATABASE_URL=$DATABASE_URL go run ./apps/ats/cmd

# SPA
make ui-ats-dev
```

Or with existing monorepo infra:

```bash
make infra-up
# create DB/user as needed, then DO_SETUP + run as above
```

Dev auth headers (SPA): `X-Profile-ID`, `X-Tenant-ID`, `X-Partition-ID`.

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
3. Inject real `MatchingTalent` / `OpportunityPublisher` / `BillingEmitter` / `Notifier`  
4. Deploy: SPA audience path for product API; SA name `service-ats` ↔ namespace `service_ats`  
5. Grant partition roles (OWNER/ADMIN/OPERATOR/MEMBER/VIEWER) so Keto `granted_*` tuples resolve  

### Permission map (summary)

| Permission | Roles (typical) |
|------------|-----------------|
| `ats_dashboard_view` | owner, admin, operator, member, viewer |
| `ats_job_manage` / `ats_publish` | owner, admin, operator |
| `ats_application_manage` | owner, admin, operator, member |
| `ats_hire` | owner, admin, operator |
| `ats_interview_manage` | owner, admin, operator, member |
| `ats_ai_use` | owner, admin, operator, member |
| `ats_demo_seed` | owner, admin, service (dev) |

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
