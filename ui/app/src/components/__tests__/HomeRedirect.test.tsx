import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render } from '@testing-library/react';
import type { AuthState } from '@stawi/auth-runtime';
import HomeRedirect from '../HomeRedirect';
import { __resetSafeNavigateForTests } from '@/utils/safeNavigate';
import type { UserContext } from '@/hooks/useUserContext';

let authState: AuthState = 'initializing';
let hasSession = false;
let ready = false;
let userCtx: UserContext;

vi.mock('@/providers/AuthProvider', () => ({
  useAuth: () => ({
    state: authState,
    hasSession,
    ready,
    login: vi.fn(),
    logout: vi.fn(),
    runtime: {},
  }),
}));

vi.mock('@/hooks/useUserContext', () => ({
  useUserContext: () => userCtx,
}));

let replaceSpy: ReturnType<typeof vi.fn>;

function stageCtx(
  partial: Partial<UserContext> & Pick<UserContext, 'stage' | 'homePath'>
): UserContext {
  return {
    label: partial.label ?? partial.stage,
    summary: partial.summary ?? '',
    dashboardAllowed: partial.dashboardAllowed ?? partial.homePath.startsWith('/dashboard'),
    onboardingAllowed: partial.onboardingAllowed ?? partial.homePath.startsWith('/onboarding'),
    entitled: partial.entitled ?? false,
    subscriptionStatus: partial.subscriptionStatus ?? null,
    readiness: partial.readiness ?? null,
    resolving: partial.resolving ?? false,
    ...partial,
  };
}

beforeEach(() => {
  __resetSafeNavigateForTests();
  replaceSpy = vi.fn();
  Object.defineProperty(window, 'location', {
    configurable: true,
    value: {
      replace: replaceSpy,
      assign: vi.fn(),
      href: 'http://localhost/',
      pathname: '/',
      search: '',
      hash: '',
    },
  });
  document.body.innerHTML = '<section id="home-hero"></section>';
  authState = 'initializing';
  hasSession = false;
  ready = false;
  userCtx = stageCtx({
    stage: 'loading',
    homePath: '/',
    resolving: true,
  });
});

function hero() {
  return document.getElementById('home-hero') as HTMLElement;
}

describe('HomeRedirect', () => {
  it('does not redirect while initializing without a session hint', () => {
    authState = 'initializing';
    hasSession = false;
    ready = false;
    render(<HomeRedirect />);
    expect(replaceSpy).not.toHaveBeenCalled();
  });

  it('keeps hero hidden while initializing with sticky session (no flash)', () => {
    authState = 'initializing';
    hasSession = true;
    ready = false;
    hero().style.display = 'none';
    render(<HomeRedirect />);
    expect(hero().style.display).toBe('none');
    expect(replaceSpy).not.toHaveBeenCalled();
  });

  it('waits while user context is resolving', () => {
    authState = 'authenticated';
    hasSession = true;
    ready = true;
    userCtx = stageCtx({ stage: 'loading', homePath: '/', resolving: true });
    render(<HomeRedirect />);
    expect(replaceSpy).not.toHaveBeenCalled();
  });

  it('sends entitled users to dashboard stage home', () => {
    authState = 'authenticated';
    hasSession = true;
    ready = true;
    userCtx = stageCtx({
      stage: 'dashboard_ready',
      homePath: '/dashboard/',
      entitled: true,
      label: 'Matching active',
    });
    render(<HomeRedirect />);
    expect(hero().style.display).toBe('none');
    expect(replaceSpy).toHaveBeenCalledWith('/dashboard/');
  });

  it('sends unpaid intake users to onboarding', () => {
    authState = 'authenticated';
    hasSession = true;
    ready = true;
    userCtx = stageCtx({
      stage: 'onboarding_intake',
      homePath: '/onboarding/',
      onboardingAllowed: true,
      label: 'Profile setup',
    });
    render(<HomeRedirect />);
    expect(replaceSpy).toHaveBeenCalledWith('/onboarding/');
  });

  it('sends subscription_error to dashboard home', () => {
    authState = 'authenticated';
    hasSession = true;
    ready = true;
    userCtx = stageCtx({
      stage: 'subscription_error',
      homePath: '/dashboard/',
      label: 'Account check failed',
    });
    render(<HomeRedirect />);
    expect(replaceSpy).toHaveBeenCalledWith('/dashboard/');
  });

  it('reveals the hero when unauthenticated', () => {
    authState = 'unauthenticated';
    hasSession = false;
    ready = true;
    hero().style.display = 'none';
    render(<HomeRedirect />);
    expect(hero().style.display).toBe('');
    expect(replaceSpy).not.toHaveBeenCalled();
  });
});
