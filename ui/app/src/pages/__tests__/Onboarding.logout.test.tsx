import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { AuthState } from '@stawi/auth-runtime';
import Onboarding from '../Onboarding';

// Controllable auth state — each test sets `authState` then (re)renders.
const login = vi.fn(() => Promise.resolve());
let authState: AuthState = 'unauthenticated';

vi.mock('@/providers/AuthProvider', () => ({
  useAuth: () => ({ state: authState, login, runtime: {}, logout: vi.fn() }),
}));

vi.mock('@/i18n/I18nProvider', () => ({
  useI18n: () => ({
    lang: 'en',
    setLang: vi.fn(),
    t: (k: string) => k,
    dir: 'ltr',
    labelFor: (c: string) => c,
    languages: ['en'],
  }),
}));

vi.mock('@/api/candidates', () => ({
  submitOnboarding: vi.fn(),
  uploadCV: vi.fn(),
  createCheckout: vi.fn(),
  fetchOnboardingDraft: vi.fn(() => Promise.resolve({ step: 1, fields: {} })),
  saveOnboardingDraft: vi.fn(),
  fetchMeSubscription: vi.fn(() => Promise.resolve({ status: 'none' })),
}));

function renderOnboarding() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <Onboarding />
    </QueryClientProvider>
  );
}

let replaceSpy: ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.clearAllMocks();
  replaceSpy = vi.fn();
  Object.defineProperty(window, 'location', {
    configurable: true,
    value: {
      href: 'http://localhost/onboarding/',
      pathname: '/onboarding/',
      search: '',
      replace: replaceSpy,
      assign: vi.fn(),
    },
  });
});

afterEach(() => {
  authState = 'unauthenticated';
});

describe('Onboarding auth handling', () => {
  it('starts sign-in for a fresh anonymous visitor', async () => {
    authState = 'unauthenticated';
    renderOnboarding();
    await waitFor(() => expect(login).toHaveBeenCalledTimes(1));
    expect(replaceSpy).not.toHaveBeenCalled();
  });

  it('does NOT bounce back to login after the user logs out (goes home instead)', async () => {
    // Mounted while authenticated...
    authState = 'authenticated';
    const { rerender } = renderOnboarding();

    // ...then the user signs out: auth flips to unauthenticated.
    authState = 'unauthenticated';
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    rerender(
      <QueryClientProvider client={qc}>
        <Onboarding />
      </QueryClientProvider>
    );

    await waitFor(() => expect(replaceSpy).toHaveBeenCalledWith('/'));
    expect(login).not.toHaveBeenCalled();
  });
});
