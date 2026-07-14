import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, waitFor, screen, fireEvent } from '@testing-library/react';
import type { AuthState } from '@stawi/auth-runtime';
import AuthCallback, { __resetAuthCallbackCompletionForTests } from '../AuthCallback';

const completeRedirect = vi.fn();
const getState = vi.fn((): AuthState => 'unauthenticated');
const onAuthStateChange = vi.fn((cb: (s: AuthState) => void) => {
  // Immediately report current state so waitUntilAuthed can settle.
  cb(getState());
  return () => {};
});
const login = vi.fn(() => Promise.resolve());

vi.mock('@/auth/runtime', () => ({
  authRuntime: () => ({
    completeRedirect,
    getState,
    onAuthStateChange,
  }),
}));

vi.mock('@/providers/AuthProvider', () => ({
  useAuth: () => ({
    login,
    state: getState(),
    logout: vi.fn(),
    runtime: {},
  }),
}));

vi.mock('@/api/candidates', () => ({
  fetchMeSubscription: vi.fn(async () => ({
    status: 'active',
    plan: 'pro',
    renews_at: null,
    cancel_at: null,
  })),
}));

let replaceSpy: ReturnType<typeof vi.fn>;

beforeEach(() => {
  __resetAuthCallbackCompletionForTests();
  completeRedirect.mockReset();
  getState.mockReturnValue('unauthenticated');
  login.mockClear();
  replaceSpy = vi.fn();
  Object.defineProperty(window, 'location', {
    configurable: true,
    value: {
      search: '?code=abc&state=xyz',
      replace: replaceSpy,
      assign: vi.fn(),
      href: 'http://localhost/auth/callback/?code=abc&state=xyz',
      pathname: '/auth/callback/',
    },
  });
});

afterEach(() => {
  __resetAuthCallbackCompletionForTests();
});

describe('AuthCallback', () => {
  it('exchanges the code once and routes active users to /dashboard/', async () => {
    completeRedirect.mockResolvedValue({ returnTo: '/' });
    getState.mockReturnValue('authenticated');

    render(<AuthCallback />);
    // Strict Mode-style double mount shares the same completion promise.
    render(<AuthCallback />);

    await waitFor(() => expect(replaceSpy).toHaveBeenCalledWith('/dashboard/'));
    expect(completeRedirect).toHaveBeenCalledTimes(1);
  });

  it('still enters the app when stash is missing but tokens already exist', async () => {
    const { AuthError } = await import('@stawi/auth-runtime');
    completeRedirect.mockRejectedValue(
      new AuthError('OAUTH_REDIRECT_STORAGE_MISSING', 'redirect stash missing')
    );
    getState.mockReturnValue('authenticated');

    render(<AuthCallback />);

    await waitFor(() => expect(replaceSpy).toHaveBeenCalledWith('/dashboard/'));
  });

  it('shows an error when exchange fails and the user is not authenticated', async () => {
    const { AuthError } = await import('@stawi/auth-runtime');
    completeRedirect.mockRejectedValue(
      new AuthError('OAUTH_REDIRECT_STORAGE_MISSING', 'redirect stash missing')
    );
    getState.mockReturnValue('unauthenticated');

    render(<AuthCallback />);

    await waitFor(
      () => expect(screen.getByText(/sign-in session expired/i)).toBeInTheDocument(),
      { timeout: 3000 }
    );
    expect(replaceSpy).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: /sign in again/i }));
    expect(login).toHaveBeenCalled();
  });
});
