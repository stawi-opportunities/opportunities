# ATS deploy checklist (`service_ats`)

Companion to [ats-runbook.md](ats-runbook.md). Use when wiring Cloud Run / colony / auth-contract.

## Service identity

| Item | Value |
|------|--------|
| Binary | `apps/ats` |
| Service name | `service_ats` |
| Connect package | `ats.v1.AtsService` |
| Audience path (catalog) | Prefer `/ats` (or existing `/jobs` if product branding keeps that host) |
| SA client / name | `service-ats` / `opportunities-ats` (must match permission namespace ownership) |
| Permission namespace | `service_ats` (from proto) |

## Setup Job (one-time / each schema+perm change)

```bash
DO_SETUP=true \
DATABASE_URL=postgres://… \
PERMISSIONS_REGISTRATION_URL=https://…/tenancy \
# + internal SA JWT env as used by other services
go run ./apps/ats/cmd
# exits after migrate + permission registration
```

Deploy knobs (cloud.deployment `frame-cloudrun-app` pattern):

- `migrate_args = ["setup"]`
- `permissions_registration = true`
- `oauth2.requestedAudiencePaths` = business peers only (`/tenancy` auto-added when registration on)

## Runtime Service

Required env:

- `DATABASE_URL` (Postgres primary; replicas via Frame config)
- OIDC / security manager for JWT
- `AUTH_REQUIRE_JWT=true` (default)
- `ATS_ENFORCE_PERMISSIONS=true` unless debugging ReBAC (default on when JWT on)

Required peer:

- `CALENDAR_SERVICE_URI=https://api.stawi.org/calendar` — **platform-calendar**
  (`service_calendar`). ATS will not start without this. Calendar is a
  platform service, not an opportunities-* app.

Optional peers (wired in `apps/ats/cmd`):

- `NOTIFICATION_SERVICE_URI` — interview email/ICS via outbox worker
- `ATS_MATCHING_DATABASE_URL` — `candidate_profiles` shortlist (else primary DB, graceful empty)
- `ATS_PRODUCT_DATABASE_URL` — dual-write published jobs to product opportunities
- `PUBLIC_SITE_URL` — invite deep links
- No SeedDemo / `ATS_AUTO_SEED` (removed)

## SPA / gateway

| Gate | Action |
|------|--------|
| Audience | Public SPA OAuth client receives product path for ATS |
| Gateway | Route e.g. `api…/ats/*` → Connect service |
| CORS | Already on service for local Vite; edge CORS for prod |

## Auth-contract / SA policy (platform)

Ensure platform SA policy for the ATS bot includes namespace grants as needed for S2S peers **it calls**. Consumers of ATS (BFF bots) need:

1. Requested audience for ATS URL  
2. Hydra `oauth_client_recipients` whitelist  
3. ReBAC grants on `service_ats` permissions they invoke  

Do **not** create per-tenant SA grants (see authentication ADR 0002 product peer mesh).

## Partition roles for humans

Recruiters need partition Access + roles that map to OPL permits for:

- OPERATOR / ADMIN: full hiring (`ats_job_manage`, `ats_hire`, …)  
- MEMBER: pipeline + interview + AI  
- VIEWER: read-only  

## Smoke after deploy

```bash
# health
curl -sS https://…/healthz

# Connect (user JWT)
curl -sS -X POST https://…/ats.v1.AtsService/GetDashboard \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -H "Connect-Protocol-Version: 1" \
  -d '{}'
```

## Out of band (other repos)

- `cloud.deployment`: `platform-calendar` (shared booking plane) then `opportunities-ats`  
- `service-authentication` auth-contract: SA policy rows if required (`service-calendar`, ATS SA)  
- DNS / gateway HTTPRoute for SPA host  

This repo ships code + runbooks; cluster manifests live in the deployments repo.
