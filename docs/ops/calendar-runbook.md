# Resource calendar runbook (`service_calendar`)

Service: `apps/calendar` · Connect: `calendar.v1.CalendarService` · Namespace: `service_calendar`  
Design: `docs/superpowers/specs/2026-08-10-resource-calendar-design.md`

## What it is

**Resource booking plane** — reserve time/capacity on anything:

| Resource type | Subject example |
|---------------|-----------------|
| `person` | `subject_kind=profile`, `subject_id=<profile_id>` |
| `room` / `property` / `equipment` | product or inventory ids |
| `custom:*` | any opaque subject |

Multi-resource bookings (panel + room + kit), hold→confirm, ICS export, external calendar sync ports.

## Local

```bash
export DATABASE_URL=postgres://postgres:postgres@127.0.0.1:5432/calendar?sslmode=disable

DO_SETUP=true DATABASE_URL=$DATABASE_URL go run ./apps/calendar/cmd

AUTH_REQUIRE_JWT=false DATABASE_URL=$DATABASE_URL HTTP_ADDR=:8096 \
  CALENDAR_MEMORY_PROVIDER=true go run ./apps/calendar/cmd
```

## Product integration

```bash
CALENDAR_SERVICE_URI=https://api.stawi.org/calendar   # platform-calendar
# Audience path: /calendar (see pkg/calendarclient)
```

Production deploy is **`platform-calendar`** in `cloud.deployment` (GCP
`stawi-platform`, platform Neon). Do not ship this binary as an
`opportunities-*` Cloud Run app.

Flow:

1. `EnsureResource` for each bookable thing  
2. `SetAvailability` weekly rules  
3. `ListSlots(resource_ids[], duration)`  
4. `CreateBooking` (hold or confirmed) with `source` + `source_ref`  
5. Optional `UpsertExternalConnection` + `TriggerSync`  

## External calendar sync

| Env | Purpose |
|-----|---------|
| `GOOGLE_CALENDAR_ENABLED=true` | Enable Google Calendar API v3 provider |
| `MICROSOFT_CALENDAR_ENABLED=true` | Enable Microsoft Graph provider |
| `CALDAV_ENABLED=true` | Enable CalDAV REPORT/PUT/DELETE |
| `CALENDAR_MEMORY_PROVIDER=true` | In-process provider for tests/dev |
| `CALENDAR_SYNC_POLL_SECONDS` | Export outbox drain (default 60) |

**Per-connection credentials** (`UpsertExternalConnection.credentials_json`):

```json
{"access_token":"…"} 
// CalDAV also: {"base_url":"https://…/cal/","username":"…","password":"…"}
```

Providers implement `business.ExternalProvider` (import busy, export booking, delete).  
Confirmed bookings enqueue `cal_sync_outbox`; worker drains to providers that are `Ready()`.

ICS always available via `GetBookingICS` without external accounts.

## ATS integration (required)

ATS **will not start** without `CALENDAR_SERVICE_URI`. There is no local slot fallback.

```bash
# Calendar first
AUTH_REQUIRE_JWT=false HTTP_ADDR=:8096 go run ./apps/calendar/cmd

# ATS
export CALENDAR_SERVICE_URI=http://127.0.0.1:8096
export CALENDAR_SERVICE_DIRECT=true
AUTH_REQUIRE_JWT=false HTTP_ADDR=:8095 go run ./apps/ats/cmd
```

ATS always:

1. Writes recruiter availability via calendar only (`subject=profile`)  
2. Lists interview slots via multi-resource `ListSlots`  
3. Books panel time via `CreateBooking(source=ats, source_ref=interview:…)`  
4. Persists `calendar_booking_id` on the interview row

## Setup vs runtime

- Setup Job: migrate + register `service_calendar` permissions  
- Runtime: Connect + sync worker; no migrate  

## Tests

```bash
go test ./apps/calendar/... -count=1 -timeout 10m
```
