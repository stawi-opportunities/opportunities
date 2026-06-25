import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render } from '@testing-library/react';
import type { AuthState } from '@stawi/auth-runtime';
import HomeRedirect from '../HomeRedirect';

let authState: AuthState = 'initializing';

vi.mock('@/providers/AuthProvider', () => ({
  useAuth: () => ({ state: authState, login: vi.fn(), logout: vi.fn(), runtime: {} }),
}));

let replaceSpy: ReturnType<typeof vi.fn>;

beforeEach(() => {
  replaceSpy = vi.fn();
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

  it('hides the hero and redirects to /dashboard/ when authenticated', () => {
    authState = 'authenticated';
    render(<HomeRedirect />);
    expect(hero().style.display).toBe('none');
    expect(replaceSpy).toHaveBeenCalledWith('/dashboard/');
  });

  it('reveals the hero (correcting a stale hint) when unauthenticated', () => {
    authState = 'unauthenticated';
    hero().style.display = 'none'; // inline script hid it from a stale hint
    render(<HomeRedirect />);
    expect(hero().style.display).toBe('');
    expect(replaceSpy).not.toHaveBeenCalled();
  });
});
