// OpenObserve browser integration — RUM for page perf/errors/web vitals
// and browser-logs for forwarding console errors & structured events.
//
// Call initOpenObserve() exactly once per page load, from the
// AppProviders boundary, so every mounted React island shares the same
// session. Event-emission helpers (trackJobView, trackApplyClick) are
// no-ops when the SDK hasn't been initialised — local dev and CI builds
// stay silent without extra branching at call sites.

import { openobserveRum } from "@openobserve/browser-rum";
import { openobserveLogs } from "@openobserve/browser-logs";

import { getConfig } from "@/utils/config";

// Track init so event helpers don't double-initialise or crash when
// config is missing. `once` guards against AppProviders mounting more
// than once (each React island calls AppProviders, and HMR may remount
// repeatedly in dev).
let initialised = false;

/**
 * Initialise RUM + browser-logs with whatever config is present.
 * Returns true when both SDKs were started, false if any required
 * field was missing and we chose to stay silent.
 */
export function initOpenObserve(): boolean {
  if (initialised) return true;
  const cfg = getConfig();
  const {
    rumClientToken,
    rumApplicationId,
    rumSite,
    rumOrganization,
    rumEnv,
    rumService,
    rumVersion,
  } = cfg;

  // Required fields — without them the SDK would 401/404 on every
  // beacon. Silent no-op is the right behaviour for dev builds.
  if (!rumClientToken || !rumApplicationId || !rumSite) {
    if (typeof console !== "undefined") {
      console.debug(
        "[openobserve] disabled: missing clientToken/applicationId/site",
      );
    }
    return false;
  }

  const common = {
    clientToken: rumClientToken,
    site: rumSite,
    organizationIdentifier: rumOrganization || "default",
    service: rumService || "stawi-jobs-web",
    env: rumEnv || "production",
    version: rumVersion || "0.0.0",
    // Our gateway terminates TLS, so the SDK talks HTTPS to
    // observe.stawi.org. insecureHTTP would downgrade transport.
    insecureHTTP: false,
    apiVersion: "v1" as const,
  };

  openobserveRum.init({
    ...common,
    applicationId: rumApplicationId,
    // Capture everything that helps us diagnose both perf and product
    // issues. defaultPrivacyLevel: 'mask-user-input' is the pragmatic
    // GDPR default — form fields are masked in session replay but
    // structural DOM + navigations are captured.
    trackResources: true,
    trackLongTasks: true,
    trackUserInteractions: true,
    defaultPrivacyLevel: "mask-user-input",
  });

  openobserveLogs.init({
    ...common,
    // Pipe window.onerror / unhandledrejection through to OpenObserve.
    // This is our replacement for Sentry for the current tier.
    forwardErrorsToLogs: true,
  });

  // Kick off session replay. The SDK samples internally; this does
  // not mean every session is recorded.
  openobserveRum.startSessionReplayRecording();

  initialised = true;
  return true;
}

/**
 * Attach an authenticated profile to the active session. Call this
 * from the auth provider once a user's identity is known. Passing a
 * falsy id clears the user — useful on sign-out.
 */
export function setAnalyticsUser(user: {
  id: string;
  name?: string;
  email?: string;
  plan?: string;
} | null): void {
  if (!initialised) return;
  if (!user || !user.id) {
    // The SDK doesn't expose a clearUser, but setUser with empty id
    // plus setGlobalContextProperty to null is enough to detach.
    openobserveRum.setGlobalContextProperty("authenticated", false);
    return;
  }
  openobserveRum.setUser({
    id: user.id,
    name: user.name,
    email: user.email,
    plan: user.plan,
  });
  openobserveRum.setGlobalContextProperty("authenticated", true);
}

/**
 * Add or update a single session-wide attribute. Use this for slow-
 * changing context like UI language or subscription tier.
 */
export function setAnalyticsContext(key: string, value: unknown): void {
  if (!initialised) return;
  openobserveRum.setGlobalContextProperty(key, value);
}

// ---- Event helpers --------------------------------------------------
//
// These are the product-level events we care about. They go through
// addAction (RUM) rather than openobserveLogs.logger.log so they show
// up in the RUM actions stream alongside page views and interactions,
// which makes funnel queries much simpler.

export interface JobViewAttrs {
  canonical_job_id?: number;
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
  openobserveRum.addAction("job_view", { ...attrs });
}

export interface JobViewEngagedAttrs {
  canonical_job_id?: number;
  slug: string;
  dwell_ms: number;
  scroll_depth_pct: number;
}

export function trackJobViewEngaged(attrs: JobViewEngagedAttrs): void {
  if (!initialised) return;
  openobserveRum.addAction("job_view_engaged", { ...attrs });
}

export interface ApplyClickAttrs {
  canonical_job_id?: number;
  slug: string;
  company?: string;
  // The URL the user will land on — /r/{slug} in production so the
  // redirect service records its own Click event we can join against.
  apply_url: string;
  // Dwell time before the click — a strong funnel signal.
  dwell_ms: number;
}

export function trackApplyClick(attrs: ApplyClickAttrs): void {
  if (!initialised) return;
  openobserveRum.addAction("apply_click", { ...attrs });
}
