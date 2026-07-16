import type { AuthState } from '@stawi/auth-runtime';

/**
 * Auth UI helpers.
 *
 * The runtime uses a fine-grained state machine:
 *   initializing → authenticated | unauthenticated
 *   authenticated → refreshing → authenticated | unauthenticated
 *
 * Components that branch on "are we signed in?" must NOT treat `refreshing`
 * (or a mid-flight `initializing` after we already knew the user was signed
 * in) as signed-out — that produces the Sign-in / avatar flicker on every
 * token refresh and during island remounts.
 */

/** True while we still have a live session (including silent token refresh). */
export function isSessionPresent(state: AuthState): boolean {
  return state === 'authenticated' || state === 'refreshing';
}

/**
 * True once auth has settled enough to render a definitive signed-in or
 * signed-out UI. While false, show a skeleton / previous paint — never
 * the opposite of what the user was a moment ago.
 */
export function isAuthSettled(state: AuthState): boolean {
  return state === 'authenticated' || state === 'unauthenticated' || state === 'refreshing';
}

/** True only when we know the user is anonymous (safe to show Sign in CTA). */
export function isDefinitelySignedOut(state: AuthState): boolean {
  return state === 'unauthenticated';
}

/** True while discovery / session restore is still in flight. */
export function isAuthPending(state: AuthState): boolean {
  return state === 'initializing' || state === 'error';
}
