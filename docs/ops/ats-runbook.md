# Employer ATS runbook

Service: `apps/ats` · UI: `ui/ats` · Design: `docs/superpowers/specs/2026-08-10-employer-ats-design.md`

## Local (useful in under a minute)

```bash
# API — sqlite, auto seed, no OIDC
make run-ats

# SPA (another terminal)
make ui-ats-dev
# open http://localhost:5175
```

Dev auth headers (set by SPA):

| Header | Default |
|--------|---------|
| `X-Profile-ID` | `dev-recruiter` |
| `X-Tenant-ID` | `dev-tenant` |
| `X-Partition-ID` | `dev-partition` |

## Recruiter happy path

1. **Today** — dashboard stats + attention checklist (seed creates sample jobs).
2. **Jobs** — create role; optional **Publish** (local projection id until opportunities publisher is wired).
3. **Pipeline** — Stawi talent shortlist → add → **AI screen** → **Advance** → **Schedule** → pick slot.
4. **More** — set Mon–Fri availability if booking returns no slots.
5. **Hire** from offer stage emits results-billing ref (`result_hire_<application_id>`).

## API surface (authenticated)

- `GET /v1/dashboard`
- `POST /v1/demo/seed`
- `GET|POST /v1/jobs`, `PATCH /v1/jobs/{id}`, publish/unpublish/close
- `…/applications`, advance, hire, interviews
- `GET /v1/jobs/{id}/talent`, `POST` add talent
- `GET/PUT /v1/me/availability`
- `GET /v1/interviews/{id}/slots`, `POST …/book`, `GET …/ics`
- `POST /v1/ai/applications/{id}/screen-summary`

## Production notes

- Prefer Postgres via Frame `DATABASE_URL`; leave `ATS_SQLITE_PATH` empty.
- `AUTH_REQUIRE_JWT=true` + OIDC; claims must include `tenant_id`, `partition_id`, `profile_id`/`sub`.
- Replace default `DemoTalent` / `LocalPublisher` / `RecordingBilling` with platform clients (matching KNN, opportunities projection, payment ledger).
- Register permission namespace and SPA audience at deploy (tenancy permission registration).
- Candidate self-serve: same API with candidate’s `profile_id` JWT; `GET /v1/me/applications` + book slots.

## Health

`GET /healthz` → `{ "status": "ok", "service": "ats", … }`
