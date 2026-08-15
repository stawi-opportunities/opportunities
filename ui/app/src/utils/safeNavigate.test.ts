import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { __resetSafeNavigateForTests, safeReplace } from './safeNavigate';

describe('safeReplace', () => {
  let replaceSpy: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    __resetSafeNavigateForTests();
    replaceSpy = vi.fn();
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: {
        pathname: '/',
        search: '',
        hash: '',
        replace: replaceSpy,
        href: 'http://localhost/',
      },
    });
  });

  afterEach(() => {
    __resetSafeNavigateForTests();
  });

  it('navigates to the requested path', () => {
    safeReplace('/dashboard/');
    expect(replaceSpy).toHaveBeenCalledWith('/dashboard/');
  });

  it('breaks onboarding↔dashboard thrash by settling on dashboard', () => {
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: {
        pathname: '/onboarding/',
        search: '',
        hash: '',
        replace: replaceSpy,
        href: 'http://localhost/onboarding/',
      },
    });
    safeReplace('/dashboard/');
    expect(replaceSpy).toHaveBeenCalledWith('/dashboard/');

    // Simulate landing on dashboard then immediately trying onboarding again.
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: {
        pathname: '/dashboard/',
        search: '',
        hash: '',
        replace: replaceSpy,
        href: 'http://localhost/dashboard/',
      },
    });
    replaceSpy.mockClear();
    safeReplace('/onboarding/');
    // Thrash protection: stay on dashboard (no navigation).
    expect(replaceSpy).not.toHaveBeenCalled();
  });
});
