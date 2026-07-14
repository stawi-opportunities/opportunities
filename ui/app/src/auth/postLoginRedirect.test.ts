import { describe, expect, it } from 'vitest';
import { resolvePostLoginPath, sanitizeReturnTo } from './postLoginRedirect';

describe('sanitizeReturnTo', () => {
  it('defaults empty / invalid to /', () => {
    expect(sanitizeReturnTo('')).toBe('/');
    expect(sanitizeReturnTo(null)).toBe('/');
    expect(sanitizeReturnTo('https://evil.com/phish')).toBe('/');
    expect(sanitizeReturnTo('//evil.com')).toBe('/');
  });

  it('keeps relative paths, query, and hash', () => {
    expect(sanitizeReturnTo('/jobs/foo/')).toBe('/jobs/foo/');
    expect(sanitizeReturnTo('/onboarding/?plan=pro')).toBe('/onboarding/?plan=pro');
    expect(sanitizeReturnTo('/dashboard/#billing')).toBe('/dashboard/#billing');
  });
});

describe('resolvePostLoginPath', () => {
  it('always sends active users to the dashboard app surface', () => {
    expect(resolvePostLoginPath('/', 'active')).toBe('/dashboard/');
    expect(resolvePostLoginPath('/jobs/rust-dev/', 'active')).toBe('/dashboard/');
    expect(resolvePostLoginPath('/search/?q=rust', 'active')).toBe('/dashboard/');
    expect(resolvePostLoginPath('/onboarding/?plan=pro', 'active')).toBe('/dashboard/');
    expect(resolvePostLoginPath('/auth/callback/', 'active')).toBe('/dashboard/');
  });

  it('preserves dashboard section hash for active users', () => {
    expect(resolvePostLoginPath('/dashboard/#matches', 'active')).toBe('/dashboard/#matches');
    expect(resolvePostLoginPath('/dashboard/', 'active')).toBe('/dashboard/');
  });

  it('always sends unpaid users to onboarding', () => {
    expect(resolvePostLoginPath('/', 'none')).toBe('/onboarding/');
    expect(resolvePostLoginPath('/dashboard/', 'none')).toBe('/onboarding/');
    expect(resolvePostLoginPath('/jobs/rust-dev/', 'none')).toBe('/onboarding/');
    expect(resolvePostLoginPath('/auth/callback/', 'none')).toBe('/onboarding/');
  });

  it('preserves onboarding plan query for unpaid users', () => {
    expect(resolvePostLoginPath('/onboarding/?plan=pro', 'none')).toBe('/onboarding/?plan=pro');
  });
});
