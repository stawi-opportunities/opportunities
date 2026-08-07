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
  it('treats active, past_due, and trial as paid', () => {
    expect(isPaidSubscriptionStatus('active')).toBe(true);
    expect(isPaidSubscriptionStatus('past_due')).toBe(true);
    expect(isPaidSubscriptionStatus('trial')).toBe(true);
  });

  it('rejects unpaid statuses', () => {
    expect(isPaidSubscriptionStatus('none')).toBe(false);
    expect(isPaidSubscriptionStatus('canceled')).toBe(false);
    expect(isPaidSubscriptionStatus('cancelled')).toBe(false);
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
    ).toEqual({ allowed: false, block: false, error: false, shouldRedirect: false });
  });

  it('blocks while subscription is loading (no dashboard paint)', () => {
    expect(evaluateSubscriptionAccess({ ...base, loading: true })).toEqual({
      allowed: false,
      block: true,
      error: false,
      shouldRedirect: false,
    });
  });

  it('allows active subscription', () => {
    expect(evaluateSubscriptionAccess({ ...base, status: 'active' })).toEqual({
      allowed: true,
      block: false,
      error: false,
      shouldRedirect: false,
    });
  });

  it('redirects unpaid without allowing dashboard', () => {
    expect(evaluateSubscriptionAccess({ ...base, status: 'none' })).toEqual({
      allowed: false,
      block: true,
      error: false,
      shouldRedirect: true,
    });
    expect(evaluateSubscriptionAccess({ ...base, status: 'canceled' })).toMatchObject({
      allowed: false,
      block: true,
      shouldRedirect: true,
    });
  });

  it('allows billing return even when still unpaid', () => {
    expect(
      evaluateSubscriptionAccess({
        ...base,
        billingReturn: true,
        status: 'none',
      })
    ).toEqual({ allowed: true, block: false, error: false, shouldRedirect: false });
  });

  it('blocks on verify error without cached status (no free dashboard)', () => {
    expect(evaluateSubscriptionAccess({ ...base, error: true, status: undefined })).toEqual({
      allowed: false,
      block: true,
      error: true,
      shouldRedirect: false,
    });
  });

  it('uses cached paid status even if a later refetch errors', () => {
    expect(evaluateSubscriptionAccess({ ...base, error: true, status: 'active' })).toEqual({
      allowed: true,
      block: false,
      error: false,
      shouldRedirect: false,
    });
  });
});
