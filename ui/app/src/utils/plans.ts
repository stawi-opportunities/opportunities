// Single source of truth for subscription tiers. The Hugo pricing cards,
// the onboarding plan picker, and the dashboard tier-specific surfaces all
// read from this file. Two paid tiers only — Starter ($10) and Managed ($200).

export type PlanId = 'starter' | 'managed';

export interface Plan {
  id: PlanId;
  name: string;
  /** Monthly price in USD. */
  price: number;
  tagline: string;
  /** Matches queued per week. `null` = unlimited discovery. */
  matchesPerWeek: number | null;
  /** Feature bullets shown in the pricing card. */
  features: string[];
  /** Labelled meta for the comparison table. */
  meta: {
    queuePriority: 'standard' | 'agent';
    support: 'email' | 'dedicated-agent';
    autoApply: boolean;
    interviewPrep: boolean;
    jobNotifications: boolean;
  };
  /** Renders the card with the "most popular" emphasis. */
  highlight?: boolean;
  /** Hero CTA copy for the pricing card. */
  ctaLabel: string;
}

export const PLANS: Plan[] = [
  {
    id: 'starter',
    name: 'Starter',
    price: 10,
    tagline: 'AI-matched jobs and digests. You apply yourself.',
    matchesPerWeek: 5,
    features: [
      'Upload your CV once — we parse and learn what fits',
      'Up to 5 AI matches per week as roles open',
      'Email digests on your schedule (daily or weekly)',
      'Browse and apply manually from your dashboard',
      'No auto-apply and no interview preparation',
    ],
    meta: {
      queuePriority: 'standard',
      support: 'email',
      autoApply: false,
      interviewPrep: false,
      jobNotifications: true,
    },
    ctaLabel: 'Start for $10/month',
  },
  {
    id: 'managed',
    name: 'Managed',
    price: 200,
    tagline: 'Unlimited discovery, auto applications, and job notifications.',
    matchesPerWeek: null,
    features: [
      'Unlimited discovery — every strong fit surfaces',
      'Auto applications on your behalf',
      'Proactive notifications when strong roles open',
      'Interview preparation and salary coaching',
      'Dedicated agent and weekly 1:1 strategy',
    ],
    meta: {
      queuePriority: 'agent',
      support: 'dedicated-agent',
      autoApply: true,
      interviewPrep: true,
      jobNotifications: true,
    },
    highlight: true,
    ctaLabel: 'Go Managed — $200/month',
  },
];

export function planById(id: PlanId): Plan {
  const p = PLANS.find((x) => x.id === id);
  if (!p) throw new Error(`unknown plan: ${id}`);
  return p;
}

/** Normalise a server-provided plan string into our enum; anything that
 * doesn't map (including legacy "free") becomes `null`, meaning "the user
 * has not completed payment for a subscription yet".
 * Legacy "pro" maps to managed (auto-apply + unlimited). */
export function normalizePlan(raw: string | null | undefined): PlanId | null {
  if (raw === 'starter' || raw === 'managed') return raw;
  if (raw === 'pro') return 'managed';
  return null;
}
