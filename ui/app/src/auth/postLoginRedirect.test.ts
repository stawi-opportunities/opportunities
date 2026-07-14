import { describe, expect, it } from 'vitest';
import {
  isPublicContentPath,
  resolvePostLoginPath,
  sanitizeReturnTo,
} from './postLoginRedirect';

describe('sanitizeReturnTo', () => {
  it('defaults empty / invalid to /', () => {
    expect(sanitizeReturnTo('')).toBe('/');
    expect(sanitizeReturnTo(null)).toBe('/');
    expect(sanitizeReturnTo('https://evil.com/phish')).toBe('/');
    expect(sanitizeReturnTo('//evil.com')).toBe('/');
  });

  it('keeps relative paths and query strings', () => {
    expect(sanitizeReturnTo('/jobs/foo/')).toBe('/jobs/foo/');
    expect(sanitizeReturnTo('/onboarding/?plan=pro')).toBe('/onboarding/?plan=pro');
    expect(sanitizeReturnTo('/dashboard/#billing')).toBe('/dashboard/#billing');
  });
});

describe('isPublicContentPath', () => {
  it('treats job/search pages as public and home as not', () => {
    expect(isPublicContentPath('/jobs/abc/')).toBe(true);
    expect(isPublicContentPath('/search/?q=rust')).toBe(true);
    expect(isPublicContentPath('/')).toBe(false);
    expect(isPublicContentPath('/dashboard/')).toBe(false);
  });
});

describe('resolvePostLoginPath', () => {
  it('sends paid users from home/onboarding to dashboard', () => {
    expect(resolvePostLoginPath('/', 'active')).toBe('/dashboard/');
    expect(resolvePostLoginPath('/onboarding/?plan=pro', 'active')).toBe('/dashboard/');
  });

  it('restores deep links for paid users', () => {
    expect(resolvePostLoginPath('/jobs/rust-dev/', 'active')).toBe('/jobs/rust-dev/');
    expect(resolvePostLoginPath('/dashboard/#matches', 'active')).toBe('/dashboard/#matches');
  });

  it('sends unpaid users to onboarding by default', () => {
    expect(resolvePostLoginPath('/', 'none')).toBe('/onboarding/');
    expect(resolvePostLoginPath('/dashboard/', 'none')).toBe('/onboarding/');
  });

  it('preserves onboarding plan query for unpaid users', () => {
    expect(resolvePostLoginPath('/onboarding/?plan=pro', 'none')).toBe('/onboarding/?plan=pro');
  });

  it('returns unpaid users to the public job they were viewing', () => {
    expect(resolvePostLoginPath('/jobs/rust-dev/', 'none')).toBe('/jobs/rust-dev/');
  });

  it('never lands on /auth/callback/', () => {
    expect(resolvePostLoginPath('/auth/callback/', 'active')).toBe('/dashboard/');
    expect(resolvePostLoginPath('/auth/callback/?code=x', 'none')).toBe('/onboarding/');
  });
});
