import { describe, it, expect } from 'vitest';
import { isBillingReturnPath } from './useSubscriptionGate';

describe('isBillingReturnPath', () => {
  it('allows Flutterwave / checkout recovery query params', () => {
    expect(isBillingReturnPath('?billing=success')).toBe(true);
    expect(isBillingReturnPath('?billing=pending&prompt_id=abc')).toBe(true);
    expect(isBillingReturnPath('?billing=failed')).toBe(true);
  });

  it('rejects ordinary dashboard visits', () => {
    expect(isBillingReturnPath('')).toBe(false);
    expect(isBillingReturnPath('?tab=subscription')).toBe(false);
    expect(isBillingReturnPath('?billing=other')).toBe(false);
  });
});
