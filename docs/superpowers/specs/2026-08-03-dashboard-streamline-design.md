# Dashboard streamline design

**Date:** 2026-08-03  
**Status:** Approved  
**Branch target:** `main` via feature branch

## Goal

Make the signed-in dashboard useful for applying to jobs: scored matches first for subscribers, a real CV hub (score / improve / export / prefs), job fitness on the job page, and subscription management only under Settings.

## Navigation (Approach A)

| Section | Hash | Role |
|---------|------|------|
| Matches | `#matches` | Default. Scored shortlist + apply. Optional “All” browse filter. |
| CV | `#cv` | CV upload/status, ATS score + diffs, improve loop, export HTML/PDF, match preferences. |
| Saved | `#saved` | Bookmarks. |
| Applications | `#applications` | Pipeline. |
| Settings | `#settings` | Profile, notifications, security, account, theme, **Subscription**. |

**Removed from top-level nav:** Tools, Feed, Preferences, Billing.

**Legacy hash redirects:**

| From | To |
|------|-----|
| `#tools`, `#preferences` | `#cv` |
| `#billing` | `#settings` (subscription tab) |
| `#feed`, `#overview`, empty | `#matches` |

## Matches

- Subscribers (`active` / `trial` / `past_due`): primary list is scored matches with Apply CTA, score, company, deadline; Find matches refresh; weekly budget strip.
- Free proof: capped shortlist + upgrade CTA.
- Empty states link to **CV** (not Preferences).
- “All opportunities” is a toggle/filter on this page (replaces Feed nav); Managed agent card remains when applicable.

## CV hub

Sections on one page:

1. Your CV — upload / version / last updated (existing candidate CV flows).
2. ATS score — `POST /matching/me/tools/cv-score`; components + priority fixes; `rewrites[]` as before/after diffs.
3. Improve — re-score after edits; surface auto-applicable fixes when present.
4. Export — template → HTML download; PDF via print stylesheet / window.print for v1.
5. Match preferences — embed existing PreferencesPanel content.

## Job fitness

- On OpportunityDetail for signed-in users: `fitJob({ opportunity_id })`.
- Remove job-fit UI from Tools; remove Tools section.

## Settings → Subscription

- Current plan, price, status, renew / cancel-at-period-end.
- Actions only: Change plan, Cancel.
- No usage charts or invoice history on this surface.
- Unpaid: CompletePaymentPanel in the same tab.

## Non-goals (v1)

- Server-side PDF generation pipeline.
- New match-ranking algorithm (use existing feed/scores).
- Redesigning applications pipeline beyond nav placement.

## Success criteria

1. Subscriber lands on scored matches and can apply without hunting.
2. CV is the only place for score / improve / export / match prefs.
3. Job page shows fitness; Tools nav is gone.
4. Subscription management is view / change plan / cancel under Settings only.
