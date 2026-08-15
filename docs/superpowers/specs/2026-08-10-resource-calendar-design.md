# Resource calendar / booking service

**Date:** 2026-08-10  
**Status:** Implemented (v1) in `apps/calendar`  
**Namespace:** `service_calendar`  
**API:** `calendar.v1.CalendarService` (Connect)

## Problem

Products need to reserve **time (and capacity)** over arbitrary things: people, rooms, equipment, property units, license pools. Profile must not own scheduling. ATS must not remain the only calendar.

## Goals

1. **Resource-first** booking: anything addressable can be registered and reserved.
2. Multi-resource bookings (panel + room + kit) with capacity-aware conflict checks.
3. Hold → confirm lifecycle for race-safe slot picks.
4. **Integration-ready external sync**: import free/busy and export bookings via provider ports (Google, Microsoft, CalDAV, …) without product rewrites.
5. Tenancy: `tenant_id` / `partition_id` on all rows; person subjects use `profile_id` only.

## Non-goals (v1)

- Full two-way Google/Microsoft OAuth UI (hooks + provider interface ship; production credentials wired per env).
- Domain meaning of *why* something is booked (ATS interview, property viewing) — products own that via `source` / `source_ref`.

## Domain

| Entity | Purpose |
|--------|---------|
| Resource | Bookable unit: type + opaque subject + capacity + timezone |
| AvailabilityRule | Weekly windows + exceptions for a resource |
| BusyBlock | Non-bookable interval (local or imported from external calendar) |
| Booking + lines | Reservation of one or more resources over `[start,end)` |
| ExternalConnection | Link resource ↔ external calendar account for sync |
| SyncOutbox | Durable push intents for export |

**Resource types (open string):** `person`, `room`, `equipment`, `property`, `vehicle`, `service_window`, `custom:*`.

**Subject:** `{ kind, id }` e.g. `kind=profile id=<profile_id>`, `kind=external id=urn:…`.

## API surface (summary)

- EnsureResource / GetResource / ListResources  
- SetAvailability / GetAvailability  
- ListSlots (multi-resource intersect, capacity)  
- CreateBooking (hold or confirmed) / ConfirmBooking / CancelBooking / GetBooking  
- ListBusy  
- UpsertExternalConnection / TriggerSync  
- GetBookingICS  

## External calendar sync

```
ExternalProvider interface
  Name() string
  ImportBusy(ctx, conn, window) ([]BusyInterval, syncToken, error)
  ExportBooking(ctx, conn, booking) (externalEventID, error)
  DeleteExport(ctx, conn, externalEventID) error
```

| Provider | v1 |
|----------|-----|
| `local` | No-op import; ICS export always available via API |
| `google` | Registered when `GOOGLE_CALENDAR_*` configured; else soft-disabled |
| `microsoft` | Same pattern for Graph |
| `caldav` | Optional generic CalDAV endpoint |

Sync worker:

1. **Import:** for connections with `import_busy=true`, pull free/busy → `BusyBlock` rows tagged `source=external:{provider}`.  
2. **Export:** drain sync outbox for confirmed bookings → provider event; store `external_event_id` on booking line/meta.  
3. Failures stay pending with attempts; product booking remains SoT.

## Product integration

- ATS (or any service): EnsureResource(person, profile_id) → ListSlots → CreateBooking(source=ats, source_ref=interview:…).  
- Optional dual-run: product keeps local UX SoT; calendar is reservation plane.  
- Client env: `CALENDAR_SERVICE_URI` + audience path `/calendar`.

## Authz

Permission namespace `service_calendar` with resource/booking/availability/sync permissions; ROLE_SERVICE for product bots.

## Setup vs runtime

Frame standard: setup Job migrates + registers permissions; runtime serves Connect + sync worker only.
