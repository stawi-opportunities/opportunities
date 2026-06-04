import {
  trackJobView,
  trackJobViewEngaged,
  trackApplyClick,
  type JobViewAttrs,
  type JobViewEngagedAttrs,
  type ApplyClickAttrs,
} from '@/analytics/posthog';

/**
 * Stable hook that surfaces the PostHog event helpers.
 *
 * Using a hook rather than direct imports keeps the analytics contract
 * in one place — if we switch providers or add consent gating, only
 * this file changes, not every call site.
 *
 * All returned functions are no-ops when PostHog isn't initialised
 * (missing API key, ad-blocker, or server-side rendering), so call
 * sites need no guard clauses.
 */
export function useAnalytics() {
  return { trackJobView, trackJobViewEngaged, trackApplyClick };
}

// Re-export the attribute types so callers can import them from the
// hook module without going through the analytics implementation layer.
export type { JobViewAttrs, JobViewEngagedAttrs, ApplyClickAttrs };
