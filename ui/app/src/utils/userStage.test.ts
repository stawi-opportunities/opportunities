import { describe, expect, it } from 'vitest';
import { pathMatchesStageHome, resolveUserStage } from './userStage';

describe('resolveUserStage', () => {
  const base = {
    authReady: true,
    hasSession: true,
    subscriptionLoading: false,
    subscriptionError: false,
    subscriptionStatus: 'none' as string | null,
    billingReturn: false,
    profileLoading: false,
    profileReady: false as boolean | null,
  };

  it('anonymous when signed out', () => {
    const s = resolveUserStage({ ...base, hasSession: false });
    expect(s.stage).toBe('anonymous');
    expect(s.homePath).toBe('/');
  });

  it('loading while auth or subscription unsettled', () => {
    expect(resolveUserStage({ ...base, authReady: false }).stage).toBe('loading');
    expect(resolveUserStage({ ...base, subscriptionLoading: true }).stage).toBe('loading');
  });

  it('onboarding_intake for unpaid incomplete', () => {
    const s = resolveUserStage({ ...base, subscriptionStatus: 'none', profileReady: false });
    expect(s.stage).toBe('onboarding_intake');
    expect(s.homePath).toBe('/onboarding/');
    expect(s.dashboardAllowed).toBe(false);
  });

  it('onboarding_paywall when unpaid but profile ready', () => {
    const s = resolveUserStage({ ...base, subscriptionStatus: 'none', profileReady: true });
    expect(s.stage).toBe('onboarding_paywall');
    expect(s.label).toMatch(/plan/i);
  });

  it('confirming_payment on billing return when not yet entitled', () => {
    const s = resolveUserStage({
      ...base,
      subscriptionStatus: 'none',
      billingReturn: true,
    });
    expect(s.stage).toBe('confirming_payment');
    expect(s.homePath).toBe('/dashboard/');
  });

  it('dashboard_setup for entitled without match-capable profile (no CV)', () => {
    const s = resolveUserStage({
      ...base,
      subscriptionStatus: 'active',
      profileReady: false,
    });
    expect(s.stage).toBe('dashboard_setup');
    expect(s.homePath).toBe('/dashboard/');
    expect(s.entitled).toBe(true);
    expect(s.label).toMatch(/CV/i);
    expect(s.label).not.toMatch(/Finish your CV/i);
  });

  it('dashboard_ready when entitled and match-capable (CV on file)', () => {
    // Preference gaps alone must not keep the user in dashboard_setup.
    const s = resolveUserStage({
      ...base,
      subscriptionStatus: 'active',
      profileReady: true,
    });
    expect(s.stage).toBe('dashboard_ready');
    expect(s.label).toMatch(/Matching/i);
  });

  it('dashboard_past_due when past_due', () => {
    const s = resolveUserStage({
      ...base,
      subscriptionStatus: 'past_due',
      profileReady: true,
    });
    expect(s.stage).toBe('dashboard_past_due');
  });

  it('subscription_error when subscription fetch failed with no status', () => {
    const s = resolveUserStage({
      ...base,
      subscriptionError: true,
      subscriptionStatus: null,
    });
    expect(s.stage).toBe('subscription_error');
    expect(s.homePath).toBe('/dashboard/');
    expect(s.onboardingAllowed).toBe(false);
    expect(s.dashboardAllowed).toBe(true); // error shell on dashboard island
    expect(s.entitled).toBe(false);
  });

  it('subscription_error for unknown status (trial, weird) — stable dashboard home', () => {
    for (const status of ['trial', 'weird', 'pending']) {
      const s = resolveUserStage({
        ...base,
        subscriptionStatus: status,
        profileReady: null,
      });
      expect(s.stage).toBe('subscription_error');
      expect(s.homePath).toBe('/dashboard/');
      expect(s.onboardingAllowed).toBe(false);
      // Must not thrash with postLogin: same status always lands on dashboard.
    }
  });

  it('cancelled/canceled map to unpaid onboarding (not subscription_error)', () => {
    for (const status of ['cancelled', 'canceled']) {
      const s = resolveUserStage({
        ...base,
        subscriptionStatus: status,
        profileReady: false,
      });
      expect(s.stage).toBe('onboarding_intake');
      expect(s.homePath).toBe('/onboarding/');
    }
  });
});

describe('pathMatchesStageHome', () => {
  it('matches islands', () => {
    expect(pathMatchesStageHome('/dashboard', '/dashboard/')).toBe(true);
    expect(pathMatchesStageHome('/onboarding/', '/onboarding/')).toBe(true);
    expect(pathMatchesStageHome('/dashboard/', '/onboarding/')).toBe(false);
  });
});

describe('stage labels are obvious once resolved', () => {
  const cases = [
    {
      name: 'intake',
      input: { subscriptionStatus: 'none', profileReady: false },
      stage: 'onboarding_intake',
      label: /profile setup/i,
    },
    {
      name: 'paywall',
      input: { subscriptionStatus: 'none', profileReady: true },
      stage: 'onboarding_paywall',
      label: /plan/i,
    },
    {
      name: 'setup',
      input: { subscriptionStatus: 'active', profileReady: false },
      stage: 'dashboard_setup',
      label: /cv/i,
    },
    {
      name: 'ready',
      input: { subscriptionStatus: 'active', profileReady: true },
      stage: 'dashboard_ready',
      label: /matching/i,
    },
    {
      name: 'past due',
      input: { subscriptionStatus: 'past_due', profileReady: true },
      stage: 'dashboard_past_due',
      label: /past due/i,
    },
  ] as const;

  it.each(cases)('$name has a distinct human label and stable home', ({ input, stage, label }) => {
    const s = resolveUserStage({
      authReady: true,
      hasSession: true,
      subscriptionLoading: false,
      subscriptionError: false,
      billingReturn: false,
      profileLoading: false,
      subscriptionStatus: input.subscriptionStatus,
      profileReady: input.profileReady,
    });
    expect(s.stage).toBe(stage);
    expect(s.label).toMatch(label);
    expect(s.summary.length).toBeGreaterThan(10);
    expect(s.homePath).toMatch(/^\/(onboarding|dashboard)\/$/);
  });
});
