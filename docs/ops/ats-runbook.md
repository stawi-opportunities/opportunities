# Employer ATS runbook

Service: `apps/ats` (Frame, **Postgres only**) · UI: `ui/ats`  
Layout follows **service-profile**: `cmd` / `config` / `migrations` / `service/{models,repository,business,handlers}` / `tests`.

Design: `docs/superpowers/specs/2026-08-10-employer-ats-design.md`

## Architecture (golang-patterns)

| Layer | Package |
|-------|---------|
| Interaction | `service/handlers` (HTTP OpenAPI for SPA/agents; thin) |
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

1. Cloud Run setup Job: `argv=["setup"]` / `DO_SETUP=true` with migration path  
2. Runtime service: Postgres only, `AUTH_REQUIRE_JWT=true`, OIDC  
3. Inject real `MatchingTalent` / `OpportunityPublisher` / `BillingEmitter` / `Notifier`  
4. Register permission namespace + SPA audience (tenancy)  
5. Optional later: Connect RPC peer surface over the same `business.Service`

## API

See handlers mount in `service/handlers/server.go` — `/v1/dashboard`, jobs, applications, talent, interviews, availability, hire, AI screen, demo seed.
