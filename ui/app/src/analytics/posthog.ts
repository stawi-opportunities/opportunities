// PostHog client-side wrapper.
//
// PostHog autocaptures pageviews, clicks, form submissions, and web
// vitals out of the box; the custom events here (`job_view`,
// `job_view_engaged`, `apply_click`) layer on top of that for the
// funnel queries we care about. Session replay is enabled by default
// — PostHog samples internally so we don't pay per-session.
//
// `initPostHog` is idempotent so AppProviders mounting once per React
// island doesn't double-init. Every helper is a no-op when the API
// key is missing — branch previews / local dev stay silent without
// per-callsite branching, matching the OpenObserve / GA4 shape this
// module replaced.

import posthog from 'posthog-js';

import { getConfig } from '@/utils/config';

let initialised = false;

/**
 * Initialise PostHog. Returns true when init ran (or already had run),
 * false when the API key is missing.
 */
export function initPostHog(): boolean {
  if (initialised) return true;
  if (typeof window === 'undefined') return false;
  const { posthogApiKey, posthogHost } = getConfig();
  if (!posthogApiKey) {
    if (typeof console !== 'undefined') {
      console.debug('[posthog] disabled: missing posthogApiKey');
    }
    return false;
  }
  posthog.init(posthogApiKey, {
    api_host: posthogHost || 'https://us.i.posthog.com',
    // Autocapture covers pageviews + clicks + form submits across every
    // React island without per-component instrumentation. The custom
    // `capture` events below are layered on top for funnel queries.
    capture_pageview: true,
    autocapture: true,
    // Web vitals (LCP, CLS, INP) — these used to live in OpenObserve
    // RUM. PostHog ships them as performance events in 1.137+.
    capture_performance: true,
    // Session replay is sampled server-side per the project's
    // recording settings; the flag here just enables eligibility.
    // Mask user input fields by default — GDPR-pragmatic default;
    // structural DOM + navigations are captured.
    session_recording: {
      maskAllInputs: true,
    },
    // Suppress posthog-js's own console banner in dev so it doesn't
    // get noisy on hot reload.
    loaded: () => {
      initialised = true;
    },
  });
  initialised = true;
  return true;
}

/**
 * Attach an authenticated profile to the active session. Passing null
 * resets the identity (use on sign-out). Mirrors the previous
 * setAnalyticsUser signature so AuthProvider didn't need a code
 * change beyond the import path.
 */
export function setAnalyticsUser(
  user: {
    id: string;
    name?: string;
    email?: string;
    plan?: string;
  } | null
): void {
  if (!initialised) return;
  if (!user || !user.id) {
    // reset() detaches the distinct_id from the device and starts a
    // fresh anonymous session — the right behaviour when the user
    // signs out.
    posthog.reset();
    return;
  }
  posthog.identify(user.id, {
    // PostHog stores these as `person properties` keyed off
    // distinct_id, so re-identifying with the same id updates the
    // existing person.
    email: user.email,
    name: user.name,
    plan: user.plan,
  });
}

/**
 * Add or update a single session-wide attribute. Stored on the
 * current person via PostHog's person properties.
 */
export function setAnalyticsContext(key: string, value: unknown): void {
  if (!initialised) return;
  posthog.setPersonProperties({ [key]: value });
}

// ---- Event helpers --------------------------------------------------
//
// Custom events go through `posthog.capture(name, properties)`. The
// event name + property keys define the schema queried in the
// PostHog UI, so keep them stable.

export interface JobViewAttrs {
  canonical_job_id?: string;
  slug: string;
  category?: string;
  company?: string;
  country?: string;
  ui_language: string;
  referrer: string;
}

export function trackJobView(attrs: JobViewAttrs): void {
  if (!initialised) return;
  posthog.capture('job_view', { ...attrs });
}

export interface JobViewEngagedAttrs {
  canonical_job_id?: string;
  slug: string;
  dwell_ms: number;
  scroll_depth_pct: number;
}

export function trackJobViewEngaged(attrs: JobViewEngagedAttrs): void {
  if (!initialised) return;
  posthog.capture('job_view_engaged', { ...attrs });
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
  posthog.capture('apply_click', { ...attrs });
}
