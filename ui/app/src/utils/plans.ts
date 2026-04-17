// Single source of truth for subscription tiers. The Hugo pricing cards,
// the onboarding plan picker, and the dashboard tier-specific surfaces all
// read from this file. There is no "Free" plan — anonymous visitors can
// still search and browse jobs without an account, but if they want
// matching delivered to them, they must pick one of the three paid tiers.

export type PlanId = "starter" | "pro" | "managed";

export interface Plan {
  id: PlanId;
  name: string;
  /** Monthly price in USD. */
  price: number;
  tagline: string;
  /** Matches queued per week. `null` = agent-managed, no numeric cap. */
  matchesPerWeek: number | null;
  /** Feature bullets shown in the pricing card. */
  features: string[];
  /** Labelled meta for the comparison table. */
  meta: {
    queuePriority: "standard" | "priority" | "agent";
    support: "email" | "dedicated-agent";
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
    id: "starter",
    name: "Starter",
    price: 10,
    tagline: "Five AI-matched jobs a week, delivered.",
    matchesPerWeek: 5,
    features: [
      "Upload your CV once — we parse and learn what fits",
      "Up to 5 matches per week, queued as jobs come in",
      "Weekly email digest of your top matches",
      "One ATS-compatibility report per month",
    ],
    meta: {
      queuePriority: "standard",
      support: "email",
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
      "Cover-letter drafts for each match",
      "Unlimited ATS reports",
      "Résumé refinement suggestions",
    ],
    meta: {
      queuePriority: "priority",
      support: "email",
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
      "A dedicated agent for the duration of your search",
      "Your agent handles applications on your behalf",
      "Weekly 1:1 strategy calls",
      "Direct line for same-day support",
      "Interview prep and salary-negotiation coaching",
    ],
    meta: {
      queuePriority: "agent",
      support: "dedicated-agent",
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

/** Normalise a server-provided plan string into our enum; anything that
 * doesn't map (including legacy "free") becomes `null`, meaning "the user
 * has not completed payment for a subscription yet". */
export function normalizePlan(raw: string | null | undefined): PlanId | null {
  if (raw === "starter" || raw === "pro" || raw === "managed") return raw;
  return null;
}
