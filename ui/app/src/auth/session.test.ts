import { describe, it, expect } from 'vitest';
import {
  isAuthPending,
  isAuthSettled,
  isDefinitelySignedOut,
  isSessionPresent,
} from './session';

describe('auth session helpers', () => {
  it('treats refreshing as a live session (no sign-out flicker)', () => {
    expect(isSessionPresent('refreshing')).toBe(true);
    expect(isSessionPresent('authenticated')).toBe(true);
    expect(isSessionPresent('unauthenticated')).toBe(false);
    expect(isSessionPresent('initializing')).toBe(false);
  });

  it('only treats unauthenticated as definite sign-out', () => {
    expect(isDefinitelySignedOut('unauthenticated')).toBe(true);
    expect(isDefinitelySignedOut('refreshing')).toBe(false);
    expect(isDefinitelySignedOut('initializing')).toBe(false);
    expect(isDefinitelySignedOut('error')).toBe(false);
  });

  it('marks initializing/error as pending', () => {
    expect(isAuthPending('initializing')).toBe(true);
    expect(isAuthPending('error')).toBe(true);
    expect(isAuthPending('authenticated')).toBe(false);
  });

  it('settled includes refreshing', () => {
    expect(isAuthSettled('refreshing')).toBe(true);
    expect(isAuthSettled('authenticated')).toBe(true);
    expect(isAuthSettled('unauthenticated')).toBe(true);
    expect(isAuthSettled('initializing')).toBe(false);
  });
});
