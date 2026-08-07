import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render } from '@testing-library/react';
import type { AuthState } from '@stawi/auth-runtime';
import HomeRedirect from '../HomeRedirect';

let authState: AuthState = 'initializing';
let hasSession = false;
let ready = false;
let subLoading = false;
let subStatus: string | undefined;

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

vi.mock('@/hooks/useSubscription', () => ({
  useSubscription: () => ({
    isLoading: subLoading,
    isError: false,
    data: subStatus != null ? { status: subStatus } : undefined,
  }),
}));

let replaceSpy: ReturnType<typeof vi.fn>;

beforeEach(() => {
  replaceSpy = vi.fn();
  Object.defineProperty(window, 'location', {
    configurable: true,
    value: { replace: replaceSpy, assign: vi.fn(), href: 'http://localhost/' },
  });
  document.body.innerHTML = '<section id="home-hero"></section>';
  authState = 'initializing';
  hasSession = false;
  ready = false;
  subLoading = false;
  subStatus = undefined;
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

  it('waits for subscription before redirecting signed-in users', () => {
    authState = 'authenticated';
    hasSession = true;
    ready = true;
    subLoading = true;
    subStatus = undefined;
    render(<HomeRedirect />);
    expect(hero().style.display).toBe('none');
    expect(replaceSpy).not.toHaveBeenCalled();
  });

  it('sends subscribed users to /dashboard/', () => {
    authState = 'authenticated';
    hasSession = true;
    ready = true;
    subStatus = 'active';
    render(<HomeRedirect />);
    expect(hero().style.display).toBe('none');
    expect(replaceSpy).toHaveBeenCalledWith('/dashboard/');
  });

  it('sends unpaid users to /onboarding/ (not dashboard)', () => {
    authState = 'authenticated';
    hasSession = true;
    ready = true;
    subStatus = 'none';
    render(<HomeRedirect />);
    expect(replaceSpy).toHaveBeenCalledWith('/onboarding/');
  });

  it('does not flash signed-out during token refresh for paid users', () => {
    authState = 'refreshing';
    hasSession = true;
    ready = true;
    subStatus = 'active';
    hero().style.display = 'none';
    render(<HomeRedirect />);
    expect(hero().style.display).toBe('none');
    expect(replaceSpy).toHaveBeenCalledWith('/dashboard/');
  });

  it('reveals the hero (correcting a stale hint) when unauthenticated', () => {
    authState = 'unauthenticated';
    hasSession = false;
    ready = true;
    hero().style.display = 'none'; // inline script hid it from a stale hint
    render(<HomeRedirect />);
    expect(hero().style.display).toBe('');
    expect(replaceSpy).not.toHaveBeenCalled();
  });
});
