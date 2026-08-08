/**
 * Canonical seeker journey stage.
 *
 * Once auth + subscription (+ optional profile readiness) are known, every
 * surface should use this stage — not invent local rules that disagree.
 *
 * Identity reminder:
 * - JWT sub = platform profile_id (person)
 * - candidate_id = job-seeker product row for that profile
 */

/** True when matching billing entitlement grants product access. */
export function isPaidSubscriptionStatus(status: string | undefined | null): boolean {
  return status === 'active' || status === 'past_due';
}

/** Checkout return / recovery on the dashboard URL. */
export function isBillingReturnPath(
  search = typeof window !== 'undefined' ? window.location.search : ''
): boolean {
  try {
    const params = new URLSearchParams(search);
    const billing = params.get('billing');
    return billing === 'success' || billing === 'pending' || billing === 'failed';
  } catch {
    return false;
  }
}

/** Ordered journey stages (not all are visited by every user). */
export type UserStage =
  | 'anonymous'
  | 'loading'
  | 'subscription_error'
  | 'confirming_payment'
  | 'onboarding_intake'
  | 'onboarding_paywall'
  | 'dashboard_setup'
  | 'dashboard_ready'
  | 'dashboard_past_due';

export type UserStageInput = {
  authReady: boolean;
  hasSession: boolean;
  /** First subscription answer still loading (no cached status). */
  subscriptionLoading: boolean;
  subscriptionError: boolean;
  subscriptionStatus: string | null | undefined;
  /** Checkout return query on dashboard (?billing=success|pending|failed). */
  billingReturn: boolean;
  /** Profile readiness still loading (only after entitled). */
  profileLoading?: boolean;
  /** True when matching profile is complete enough. null = unknown. */
  profileReady?: boolean | null;
};

export type UserStageInfo = {
  stage: UserStage;
  /** Short label for chips / banners. */
  label: string;
  /** One-line explanation of what the user should do next. */
  summary: string;
  /** Stable home path for this stage (no thrash). */
  homePath: string;
  /** User may use product dashboard chrome. */
  dashboardAllowed: boolean;
  /** User is in the pre-pay funnel. */
  onboardingAllowed: boolean;
  entitled: boolean;
};

const LABELS: Record<UserStage, { label: string; summary: string; homePath: string }> = {
  anonymous: {
    label: 'Signed out',
    summary: 'Sign in to build your job-seeker profile and subscribe.',
    homePath: '/',
  },
  loading: {
    label: 'Loading…',
    summary: 'Checking your account and subscription.',
    homePath: '/',
  },
  subscription_error: {
    label: 'Account check failed',
    summary: 'We could not verify your subscription. Retry from the dashboard.',
    homePath: '/dashboard/',
  },
  confirming_payment: {
    label: 'Confirming payment',
    summary: 'Waiting for billing to activate your subscription.',
    homePath: '/dashboard/',
  },
  onboarding_intake: {
    label: 'Profile setup',
    summary: 'Complete the intake chat and CV so we can match opportunities.',
    homePath: '/onboarding/',
  },
  onboarding_paywall: {
    label: 'Choose a plan',
    summary: 'Your profile is ready — subscribe to unlock matching.',
    homePath: '/onboarding/',
  },
  dashboard_setup: {
    label: 'Finish your CV',
    summary: 'You are subscribed. Complete CV and preferences in the hub.',
    homePath: '/dashboard/',
  },
  dashboard_ready: {
    label: 'Matching active',
    summary: 'Subscription and profile are ready — browse matches.',
    homePath: '/dashboard/',
  },
  dashboard_past_due: {
    label: 'Payment past due',
    summary: 'Update payment details to keep matching uninterrupted.',
    homePath: '/dashboard/',
  },
};

/**
 * Pure stage resolver. Prefer this over ad-hoc if/else across islands.
 */
export function resolveUserStage(input: UserStageInput): UserStageInfo {
  const {
    authReady,
    hasSession,
    subscriptionLoading,
    subscriptionError,
    subscriptionStatus,
    billingReturn,
    profileLoading = false,
    profileReady = null,
  } = input;

  if (!authReady) {
    return pack('loading');
  }
  if (!hasSession) {
    return pack('anonymous');
  }
  if (subscriptionLoading) {
    return pack('loading');
  }
  if (subscriptionError && (subscriptionStatus == null || subscriptionStatus === '')) {
    return pack('subscription_error');
  }

  const entitled = isPaidSubscriptionStatus(subscriptionStatus);
  const unpaid =
    subscriptionStatus === 'none' ||
    subscriptionStatus === 'cancelled' ||
    subscriptionStatus === 'canceled';

  if (!entitled && billingReturn) {
    return pack('confirming_payment');
  }

  if (entitled) {
    if (subscriptionStatus === 'past_due') {
      return pack('dashboard_past_due');
    }
    if (profileLoading || profileReady == null) {
      // Entitled: stay on dashboard while profile loads; do not send to onboarding.
      return pack('dashboard_setup');
    }
    if (profileReady === false) {
      return pack('dashboard_setup');
    }
    return pack('dashboard_ready');
  }

  if (unpaid) {
    // Without profile signal, treat as intake (safer than paywall).
    if (profileReady === true) {
      return pack('onboarding_paywall');
    }
    return pack('onboarding_intake');
  }

  // Unknown status: do not bounce; present as loading-like error recovery.
  return pack('subscription_error');
}

function pack(stage: UserStage): UserStageInfo {
  const meta = LABELS[stage];
  const entitled =
    stage === 'dashboard_ready' || stage === 'dashboard_setup' || stage === 'dashboard_past_due';
  const onboardingAllowed = stage === 'onboarding_intake' || stage === 'onboarding_paywall';
  return {
    stage,
    label: meta.label,
    summary: meta.summary,
    homePath: meta.homePath,
    dashboardAllowed: entitled || stage === 'confirming_payment' || stage === 'subscription_error',
    onboardingAllowed,
    entitled,
  };
}

/** Whether current browser path matches the stage home island. */
export function pathMatchesStageHome(pathname: string, homePath: string): boolean {
  const p = pathname.endsWith('/') ? pathname : `${pathname}/`;
  const h = homePath.endsWith('/') ? homePath : `${homePath}/`;
  if (h === '/') return p === '/';
  return p.startsWith(h);
}
