// Single source of truth for subscription tiers. The Hugo pricing table,
// the onboarding plan picker, and the dashboard tier-specific surfaces all
// read from this file. Change a tier here and every consumer follows.

export type PlanId = "free" | "starter" | "pro" | "managed";

export interface Plan {
  id: PlanId;
  name: string;
  /** Monthly price in USD. `null` for the free tier. */
  price: number | null;
  tagline: string;
  /** Matches queued per week. `null` = not a match-based plan. */
  matchesPerWeek: number | null;
  /** Feature bullets shown in the pricing card. First item can be "Everything in X". */
  features: string[];
  /** Labelled meta for the comparison table. */
  meta: {
    queuePriority: "none" | "standard" | "priority" | "agent";
    support: "self-serve" | "email" | "email+slack" | "dedicated-agent";
    atsReport: boolean;
    coverLetters: boolean;
    agent: boolean;
  };
  /** Renders the card with the "most popular" emphasis. */
  highlight?: boolean;
  /** Hero CTA copy for the pricing card. */
  ctaLabel: string;
}

export const PLANS: Plan[] = [
  {
    id: "free",
    name: "Free",
    price: null,
    tagline: "Search jobs yourself — forever free.",
    matchesPerWeek: null,
    features: [
      "Browse every job in the database",
      "Filter by category, region, salary, and seniority",
      "Save jobs to your dashboard",
      "Apply directly on the employer's site",
    ],
    meta: {
      queuePriority: "none",
      support: "self-serve",
      atsReport: false,
      coverLetters: false,
      agent: false,
    },
    ctaLabel: "Start browsing",
  },
  {
    id: "starter",
    name: "Starter",
    price: 10,
    tagline: "Five AI-matched jobs a week, delivered.",
    matchesPerWeek: 5,
    features: [
      "Everything in Free",
      "Upload your CV once — we parse and learn what fits",
      "Up to 5 matches per week, queued as jobs come in",
      "Weekly email digest of your top matches",
      "One ATS-compatibility report per month",
    ],
    meta: {
      queuePriority: "standard",
      support: "email",
      atsReport: true,
      coverLetters: false,
      agent: false,
    },
    ctaLabel: "Start for $10/month",
  },
  {
    id: "pro",
    name: "Pro",
    price: 50,
    tagline: "5× the matches. Priority queue. Better tools.",
    matchesPerWeek: 25,
    features: [
      "Everything in Starter",
      "Up to 25 matches per week",
      "Priority placement in the matching queue",
      "Cover-letter drafts written for each match",
      "Unlimited ATS reports",
      "Résumé refinement suggestions",
    ],
    meta: {
      queuePriority: "priority",
      support: "email",
      atsReport: true,
      coverLetters: true,
      agent: false,
    },
    highlight: true,
    ctaLabel: "Upgrade to Pro — $50/month",
  },
  {
    id: "managed",
    name: "Managed",
    price: 200,
    tagline: "A real person running your job search.",
    matchesPerWeek: null,
    features: [
      "Everything in Pro",
      "A dedicated agent for the length of your search",
      "Your agent handles applications on your behalf",
      "Weekly 1:1 strategy calls",
      "Direct line for same-day support",
      "Interview prep and salary-negotiation coaching",
    ],
    meta: {
      queuePriority: "agent",
      support: "dedicated-agent",
      atsReport: true,
      coverLetters: true,
      agent: true,
    },
    ctaLabel: "Hire an agent — $200/month",
  },
];

export function planById(id: PlanId): Plan {
  const p = PLANS.find((x) => x.id === id);
  if (!p) throw new Error(`unknown plan: ${id}`);
  return p;
}

export const PAID_PLANS = PLANS.filter((p) => p.price !== null);
