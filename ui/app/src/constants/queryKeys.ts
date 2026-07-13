// Centralised React Query cache key registry.
// Every useQuery / useMutation in the codebase must reference a key
// from here — no bare string literals. This eliminates cache-key drift
// when the same query is used across multiple components.

export const QUERY_KEYS = {
  // Candidate profile (/me)
  CANDIDATE_PROFILE: ['candidate-profile'] as const,

  // Subscription status (/me/subscription)
  SUBSCRIPTION: ['me-subscription'] as const,

  // Public job search (/api/search)
  SEARCH: (params: Record<string, unknown>) => ['search', params] as const,

  // Tiered discovery feed (/api/feed)
  FEED: (params: Record<string, unknown>) => ['feed', params] as const,

  // Per-job snapshot (Postgres via /api/jobs/{slug})
  SNAPSHOT: (prefix: string, slug: string, lang: string) =>
    ['snapshot', prefix, slug, lang] as const,

  // Billing plans (/billing/plans)
  BILLING_PLANS: ['billing-plans'] as const,
} as const;
