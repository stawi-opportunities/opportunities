import { describe, expect, it } from 'vitest';
import {
  isContentReturnPath,
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

  it('keeps relative paths, query, and hash', () => {
    expect(sanitizeReturnTo('/jobs/foo/')).toBe('/jobs/foo/');
    expect(sanitizeReturnTo('/onboarding/?plan=pro')).toBe('/onboarding/?plan=pro');
    expect(sanitizeReturnTo('/dashboard/#billing')).toBe('/dashboard/#billing');
  });
});

describe('isContentReturnPath', () => {
  it('recognizes opportunity detail paths', () => {
    expect(isContentReturnPath('/jobs/rust-dev/')).toBe(true);
    expect(isContentReturnPath('/jobs/rust-dev/?apply=1')).toBe(true);
    expect(isContentReturnPath('/scholarships/foo/')).toBe(true);
    expect(isContentReturnPath('/search/?q=rust')).toBe(false);
    expect(isContentReturnPath('/dashboard/')).toBe(false);
  });
});

describe('resolvePostLoginPath', () => {
  it('restores job detail for login-to-apply (any subscription)', () => {
    expect(resolvePostLoginPath('/jobs/rust-dev/?apply=1', 'none')).toBe(
      '/jobs/rust-dev/?apply=1'
    );
    expect(resolvePostLoginPath('/jobs/rust-dev/', 'active')).toBe('/jobs/rust-dev/');
    expect(resolvePostLoginPath('/jobs/rust-dev/?apply=1', 'active')).toBe(
      '/jobs/rust-dev/?apply=1'
    );
  });

  it('sends active users to the dashboard by default', () => {
    expect(resolvePostLoginPath('/', 'active')).toBe('/dashboard/');
    expect(resolvePostLoginPath('/search/?q=rust', 'active')).toBe('/dashboard/');
    expect(resolvePostLoginPath('/onboarding/?plan=pro', 'active')).toBe('/dashboard/');
    expect(resolvePostLoginPath('/auth/callback/', 'active')).toBe('/dashboard/');
  });

  it('preserves dashboard section hash for active users', () => {
    expect(resolvePostLoginPath('/dashboard/#matches', 'active')).toBe('/dashboard/#matches');
    expect(resolvePostLoginPath('/dashboard/', 'active')).toBe('/dashboard/');
  });

  it('sends unpaid users to onboarding when not returning to content', () => {
    expect(resolvePostLoginPath('/', 'none')).toBe('/onboarding/');
    expect(resolvePostLoginPath('/dashboard/', 'none')).toBe('/onboarding/');
    expect(resolvePostLoginPath('/auth/callback/', 'none')).toBe('/onboarding/');
  });

  it('preserves onboarding plan query for unpaid users', () => {
    expect(resolvePostLoginPath('/onboarding/?plan=pro', 'none')).toBe('/onboarding/?plan=pro');
  });
});
