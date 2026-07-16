import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render } from '@testing-library/react';
import type { AuthState } from '@stawi/auth-runtime';
import HomeRedirect from '../HomeRedirect';

let authState: AuthState = 'initializing';
let hasSession = false;
let ready = false;

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

  it('hides the hero and redirects any signed-in user to /dashboard/', () => {
    authState = 'authenticated';
    hasSession = true;
    ready = true;
    render(<HomeRedirect />);
    expect(hero().style.display).toBe('none');
    expect(replaceSpy).toHaveBeenCalledWith('/dashboard/');
  });

  it('does not flash signed-out during token refresh', () => {
    authState = 'refreshing';
    hasSession = true;
    ready = true;
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
