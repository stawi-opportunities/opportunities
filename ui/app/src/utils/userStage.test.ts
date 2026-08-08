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

  it('dashboard_setup for entitled incomplete profile', () => {
    const s = resolveUserStage({
      ...base,
      subscriptionStatus: 'active',
      profileReady: false,
    });
    expect(s.stage).toBe('dashboard_setup');
    expect(s.homePath).toBe('/dashboard/');
    expect(s.entitled).toBe(true);
  });

  it('dashboard_ready when entitled and profile complete', () => {
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
});

describe('pathMatchesStageHome', () => {
  it('matches islands', () => {
    expect(pathMatchesStageHome('/dashboard', '/dashboard/')).toBe(true);
    expect(pathMatchesStageHome('/onboarding/', '/onboarding/')).toBe(true);
    expect(pathMatchesStageHome('/dashboard/', '/onboarding/')).toBe(false);
  });
});
