import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, waitFor } from '@testing-library/react';
import type { AuthState } from '@stawi/auth-runtime';
import HomeRedirect from '../HomeRedirect';

let authState: AuthState = 'initializing';
let subStatus: 'active' | 'none' = 'active';

vi.mock('@/providers/AuthProvider', () => ({
  useAuth: () => ({ state: authState, login: vi.fn(), logout: vi.fn(), runtime: {} }),
}));

vi.mock('@/api/candidates', () => ({
  fetchMeSubscription: vi.fn(async () => ({
    status: subStatus,
    plan: null,
    renews_at: null,
    cancel_at: null,
  })),
}));

let replaceSpy: ReturnType<typeof vi.fn>;

beforeEach(() => {
  replaceSpy = vi.fn();
  subStatus = 'active';
  Object.defineProperty(window, 'location', {
    configurable: true,
    value: { replace: replaceSpy, assign: vi.fn(), href: 'http://localhost/' },
  });
  document.body.innerHTML = '<section id="home-hero"></section>';
});

function hero() {
  return document.getElementById('home-hero') as HTMLElement;
}

describe('HomeRedirect hero gating', () => {
  it('leaves the hero visible and does not redirect while initializing', () => {
    authState = 'initializing';
    render(<HomeRedirect />);
    expect(hero().style.display).toBe('');
    expect(replaceSpy).not.toHaveBeenCalled();
  });

  it('hides the hero and redirects paid users to /dashboard/', async () => {
    authState = 'authenticated';
    subStatus = 'active';
    render(<HomeRedirect />);
    expect(hero().style.display).toBe('none');
    await waitFor(() => expect(replaceSpy).toHaveBeenCalledWith('/dashboard/'));
  });

  it('sends unpaid authenticated users to /onboarding/', async () => {
    authState = 'authenticated';
    subStatus = 'none';
    render(<HomeRedirect />);
    await waitFor(() => expect(replaceSpy).toHaveBeenCalledWith('/onboarding/'));
  });

  it('reveals the hero (correcting a stale hint) when unauthenticated', () => {
    authState = 'unauthenticated';
    hero().style.display = 'none'; // inline script hid it from a stale hint
    render(<HomeRedirect />);
    expect(hero().style.display).toBe('');
    expect(replaceSpy).not.toHaveBeenCalled();
  });
});
