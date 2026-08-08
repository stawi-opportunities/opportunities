import { describe, it, expect } from 'vitest';
import {
  evaluateSubscriptionAccess,
  isBillingReturnPath,
  isPaidSubscriptionStatus,
} from './useSubscriptionGate';

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

describe('isPaidSubscriptionStatus', () => {
  it('treats active and past_due as entitled', () => {
    expect(isPaidSubscriptionStatus('active')).toBe(true);
    expect(isPaidSubscriptionStatus('past_due')).toBe(true);
  });

  it('rejects unpaid and non-confirmed statuses', () => {
    expect(isPaidSubscriptionStatus('none')).toBe(false);
    expect(isPaidSubscriptionStatus('canceled')).toBe(false);
    expect(isPaidSubscriptionStatus('cancelled')).toBe(false);
    expect(isPaidSubscriptionStatus('trial')).toBe(false); // API maps trial→active
    expect(isPaidSubscriptionStatus('')).toBe(false);
    expect(isPaidSubscriptionStatus(null)).toBe(false);
    expect(isPaidSubscriptionStatus(undefined)).toBe(false);
  });
});

describe('evaluateSubscriptionAccess', () => {
  const base = {
    authReady: true,
    hasSession: true,
    billingReturn: false,
    status: undefined as string | undefined,
    loading: false,
    error: false,
  };

  it('does not block before auth is ready', () => {
    expect(
      evaluateSubscriptionAccess({ ...base, authReady: false, hasSession: true, loading: true })
    ).toMatchObject({ allowed: false, block: false, shouldRedirect: false });
  });

  it('blocks while subscription is loading (no dashboard paint)', () => {
    expect(evaluateSubscriptionAccess({ ...base, loading: true })).toMatchObject({
      allowed: false,
      block: true,
      shouldRedirect: false,
      confirmingPayment: false,
    });
  });

  it('allows when status is active or past_due', () => {
    expect(evaluateSubscriptionAccess({ ...base, status: 'active' })).toMatchObject({
      allowed: true,
      block: false,
      error: false,
      shouldRedirect: false,
      confirmingPayment: false,
    });
    expect(evaluateSubscriptionAccess({ ...base, status: 'past_due' })).toMatchObject({
      allowed: true,
      shouldRedirect: false,
    });
  });

  it('redirects unpaid without allowing dashboard', () => {
    expect(evaluateSubscriptionAccess({ ...base, status: 'none' })).toMatchObject({
      allowed: false,
      block: true,
      shouldRedirect: true,
      confirmingPayment: false,
    });
    expect(evaluateSubscriptionAccess({ ...base, status: 'cancelled' })).toMatchObject({
      allowed: false,
      shouldRedirect: true,
    });
  });

  it('does not redirect on unknown status (avoids thrash)', () => {
    expect(evaluateSubscriptionAccess({ ...base, status: 'weird' })).toMatchObject({
      allowed: false,
      block: true,
      shouldRedirect: false,
    });
  });

  it('billing return without active status only confirms payment (no product UI)', () => {
    expect(
      evaluateSubscriptionAccess({
        ...base,
        billingReturn: true,
        status: 'none',
      })
    ).toMatchObject({
      allowed: false,
      block: true,
      error: false,
      shouldRedirect: false,
      confirmingPayment: true,
      stage: 'confirming_payment',
    });
  });

  it('billing return with active status allows dashboard', () => {
    expect(
      evaluateSubscriptionAccess({
        ...base,
        billingReturn: true,
        status: 'active',
      })
    ).toMatchObject({ allowed: true, confirmingPayment: false });
  });

  it('blocks on verify error without cached status', () => {
    expect(evaluateSubscriptionAccess({ ...base, error: true, status: undefined })).toMatchObject({
      allowed: false,
      block: true,
      error: true,
      shouldRedirect: false,
    });
  });

  it('uses cached active status even if a later refetch errors', () => {
    expect(evaluateSubscriptionAccess({ ...base, error: true, status: 'active' })).toMatchObject({
      allowed: true,
      block: false,
    });
  });
});
