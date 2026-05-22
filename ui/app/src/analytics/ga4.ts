// Google Analytics 4 client-side wrapper.
//
// The gtag.js snippet in layouts/partials/head.html stamps a global
// `window.gtag` and `window.dataLayer` BEFORE this module loads.
// `initGA4` only fires the auth-aware `config` call; the script tag is
// what actually starts measurement.
//
// Every helper here is a no-op when `gaMeasurementId` is missing —
// branch/preview builds and local dev stay silent without per-callsite
// branching. The OpenObserve module this replaces had the same shape;
// the call sites (AuthProvider, OpportunityDetail) didn't need to
// change.

import { getConfig } from "@/utils/config";

type GtagFn = (...args: unknown[]) => void;
declare global {
  interface Window {
    gtag?: GtagFn;
    dataLayer?: unknown[];
  }
}

function gtag(...args: unknown[]): void {
  if (typeof window === "undefined" || !window.gtag) return;
  window.gtag(...args);
}

let initialised = false;

/**
 * Confirm gtag.js is live and apply any post-load configuration.
 * Idempotent so AppProviders re-renders (one per React island) don't
 * double-configure. Returns true when a measurement ID is configured.
 */
export function initGA4(): boolean {
  if (initialised) return true;
  const { gaMeasurementId } = getConfig();
  if (!gaMeasurementId) {
    if (typeof console !== "undefined") {
      console.debug("[ga4] disabled: missing gaMeasurementId");
    }
    return false;
  }
  if (typeof window === "undefined" || !window.gtag) {
    if (typeof console !== "undefined") {
      console.debug("[ga4] disabled: gtag.js not loaded");
    }
    return false;
  }
  initialised = true;
  return true;
}

/**
 * Attach an authenticated profile to the active session. Pass null on
 * sign-out. Mirrors the OpenObserve setAnalyticsUser signature so the
 * AuthProvider call sites didn't need a code change.
 */
export function setAnalyticsUser(user: {
  id: string;
  name?: string;
  email?: string;
  plan?: string;
} | null): void {
  if (!initialised) return;
  if (!user || !user.id) {
    gtag("set", "user_properties", { plan: null });
    gtag("config", getConfig().gaMeasurementId, { user_id: null });
    return;
  }
  gtag("config", getConfig().gaMeasurementId, { user_id: user.id });
  // GA4 doesn't ingest name/email by policy (PII); only id + properties
  // safe for analytics. Plan is the high-value cohort dimension here.
  gtag("set", "user_properties", {
    plan: user.plan ?? null,
  });
}

/**
 * Add or update a single session-wide attribute. Use this for
 * slow-changing context like UI language or subscription tier. Stored
 * in GA4's user_properties bag.
 */
export function setAnalyticsContext(key: string, value: unknown): void {
  if (!initialised) return;
  gtag("set", "user_properties", { [key]: value });
}

// ---- Event helpers --------------------------------------------------
//
// Custom events go through `gtag('event', name, params)`. GA4 charges
// nothing for custom events but each event name and parameter is added
// to the property's schema, so keep the names stable.

export interface JobViewAttrs {
  canonical_job_id?: string;
  slug: string;
  category?: string;
  company?: string;
  country?: string;
  ui_language: string;
  snapshot_language?: string;
  translated_notice_shown: boolean;
  referrer: string;
}

export function trackJobView(attrs: JobViewAttrs): void {
  if (!initialised) return;
  gtag("event", "job_view", { ...attrs });
}

export interface JobViewEngagedAttrs {
  canonical_job_id?: string;
  slug: string;
  dwell_ms: number;
  scroll_depth_pct: number;
}

export function trackJobViewEngaged(attrs: JobViewEngagedAttrs): void {
  if (!initialised) return;
  gtag("event", "job_view_engaged", { ...attrs });
}

export interface ApplyClickAttrs {
  canonical_job_id?: string;
  slug: string;
  company?: string;
  apply_url: string;
  dwell_ms: number;
}

export function trackApplyClick(attrs: ApplyClickAttrs): void {
  if (!initialised) return;
  gtag("event", "apply_click", { ...attrs });
}
