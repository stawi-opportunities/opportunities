# Dashboard Redesign Plan

## Design Language (preserved)
- White/navy cards: `rounded-xl`, `shadow-sm ring-1 ring-gray-200`
- Typography: `text-3xl font-bold` h1, `text-lg font-semibold` titles, `text-sm` body
- Colors: Navy (`navy-900`) anchors, accent (teal) for links/CTAs, 4 accent colors for stats (blue, teal, purple, emerald)
- Layout: `max-w-6xl` centered, responsive grid
- Dark mode: already supported via `dark:` variants

---

## Phase 1: Overview → Bento Grid

### Current
Stacked vertical layout: `StatsRow` (4 stat cards) → `QuickActions` → `DashboardBanner` → `OverviewPanel` (2 sub-cards stacked)

### Target
```
┌─────────────────────────────────────────────┐
│  DashboardHeader (unchanged)                 │
├──────────────────┬──────────────────────────┤
│                  │  AI Match Summary (wide)  │
│  Stat 1   Stat 2 ├──────────────────────────┤
│                  │  Trending Today           │
│  Stat 3   Stat 4 ├──────────────────────────┤
│                  │  Quick Stats (compact)    │
├──────────────────┴──────────────────────────┤
│  Activity Timeline (full-width)             │
├──────────────────────┬───────────────────────┤
│  Quick Actions       │  Profile Completeness │
└──────────────────────┴───────────────────────┘
```

### Changes
- **StatsRow** → 2x2 grid on left column, with delta trends (e.g. `+3 this week`)
- **Spacer card "Trending Today"** — top opportunities surfaced right column
- **Activity Timeline** replaces the static "Getting Started" list with live recent actions
- **QuickActions** reduced to 1 row, paired beside ProfileCompleteness
- Files: `StatsRow.tsx`, `OverviewPanel.tsx`, new `ActivityTimeline.tsx`

---

## Phase 2: Empty States for Every Panel

### Current
Text-only: "No matches yet", "No saved jobs", "No applications"

### Target
Each panel shows a contextual illustration + 2-line message + CTA:
- **Matches**: Compass icon + "We're scanning opportunities for you. Results appear here within 24 hours."
- **Saved Jobs**: Bookmark icon + "Star opportunities as you browse to save them here."
- **Applications**: Send icon + "Apply to matches with one click. Your applications will appear here."

### Changes
- New `EmptyPanelState.tsx` component (reusable, takes icon + message + CTA)
- Files: `EmptyFeedState.tsx`, `SavedJobsPanel.tsx`, `ApplicationsPanel.tsx`, `MatchesPanel.tsx`

---

## Phase 3: Sidebar Grouped Sections

### Current
8 flat nav items in a single list.

### Target
```
Activity
  ▸ Overview
  ▸ Feed
  ▸ Matches (3)
───────────────
Manage
  ▸ Saved
  ▸ Applications
  ▸ Preferences
  ▸ Billing
  ▸ Settings
```

### Changes
- Add visual separator labels `text-xs font-semibold uppercase tracking-wider text-gray-400`
- Files: `DashboardSidebar.tsx` (the `SidebarNav` component)

---

## Phase 4: Mini Trends on StatCards

### Current
Big number + label. Static.

### Target
```
┌─────────────────────┐
│ OPPORTUNITIES       │
│ 142         ▲ +12   │
│              this wk│
└─────────────────────┘
```

### Changes
- Add optional `trend?: { delta: number; label: string }` prop to `StatCard`
- Green `▲ +12` badge when positive, gray `▬ 0` when flat, amber `▼ -3` when negative
- Files: `StatCard.tsx`, `StatsRow.tsx`

---

## Phase 5: Progressive-Disclosure Previews on Overview

### Current
Overview section is static text.

### Target
Overview shows **preview cards** for each section:
- **Feed**: latest 2 opportunities as mini cards
- **Saved**: latest 2 saved jobs
- **Applications**: latest 2 applications with status
Each preview has a "View all →" link.

### Changes
- New `SectionPreview` component or use the existing panel content truncated
- Files: `OverviewPanel.tsx` (or new `OverviewFeed.tsx`)

---

## Implementation Order

| Phase | Effort | Impact | Dependencies |
|-------|--------|--------|-------------|
| 2 (Empty states) | Low | High | None — standalone component |
| 3 (Sidebar groups) | Low | Medium | None |
| 4 (StatCard trends) | Low | Medium | None |
| 1 (Bento grid) | Medium | High | Requires layout restructure |
| 5 (Previews) | Medium | Medium | Depends on Phase 1 layout |

Total estimated effort: **5-7 days** for one developer.
