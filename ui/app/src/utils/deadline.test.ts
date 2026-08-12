import { describe, expect, it } from 'vitest';
import {
  daysUntil,
  formatDeadlineRelative,
  formatDeadlineShort,
  primaryDate,
  resolveDeadlineIso,
  urgencyForDeadline,
} from './deadline';

// Fixed "now": 2026-07-15T12:00:00Z
const NOW = Date.parse('2026-07-15T12:00:00Z');

describe('resolveDeadlineIso', () => {
  it('prefers deadline over expires_at and attributes', () => {
    expect(
      resolveDeadlineIso({
        deadline: '2026-08-01T00:00:00Z',
        expires_at: '2026-09-01T00:00:00Z',
        attributes: { expiry: '2026-10-01T00:00:00Z' },
      })
    ).toBe('2026-08-01T00:00:00Z');
  });

  it('falls back to expires_at then attributes.expiry', () => {
    expect(resolveDeadlineIso({ expires_at: '2026-09-01T00:00:00Z' })).toBe('2026-09-01T00:00:00Z');
    expect(resolveDeadlineIso({ attributes: { expiry: '2026-10-01T00:00:00Z' } })).toBe(
      '2026-10-01T00:00:00Z'
    );
  });

  it('returns null when nothing valid', () => {
    expect(resolveDeadlineIso({})).toBeNull();
    expect(resolveDeadlineIso({ deadline: 'not-a-date' })).toBeNull();
  });
});

describe('daysUntil / urgency', () => {
  it('classifies urgency bands', () => {
    expect(urgencyForDeadline('2026-07-10T00:00:00Z', NOW)).toBe('expired');
    expect(urgencyForDeadline('2026-07-15T23:00:00Z', NOW)).toBe('today');
    expect(urgencyForDeadline('2026-07-18T00:00:00Z', NOW)).toBe('urgent');
    expect(urgencyForDeadline('2026-07-22T00:00:00Z', NOW)).toBe('soon');
    expect(urgencyForDeadline('2026-08-20T00:00:00Z', NOW)).toBe('later');
    expect(urgencyForDeadline(null, NOW)).toBe('none');
  });

  it('computes whole-day distance', () => {
    expect(daysUntil('2026-07-15T00:00:00Z', NOW)).toBe(0);
    expect(daysUntil('2026-07-18T00:00:00Z', NOW)).toBe(3);
    expect(daysUntil('2026-07-10T00:00:00Z', NOW)).toBe(-5);
  });
});

describe('formatDeadlineShort / relative', () => {
  it('formats short labels', () => {
    expect(formatDeadlineShort('2026-07-10T00:00:00Z', NOW)).toBe('Expired');
    expect(formatDeadlineShort('2026-07-15T00:00:00Z', NOW)).toBe('Today');
    expect(formatDeadlineShort('2026-07-16T00:00:00Z', NOW)).toBe('1d left');
    expect(formatDeadlineShort('2026-07-20T00:00:00Z', NOW)).toBe('5d left');
  });

  it('formats relative labels', () => {
    expect(formatDeadlineRelative('2026-07-15T00:00:00Z', NOW)).toBe('today');
    expect(formatDeadlineRelative('2026-07-16T00:00:00Z', NOW)).toBe('tomorrow');
    expect(formatDeadlineRelative('2026-07-20T00:00:00Z', NOW)).toBe('in 5 days');
  });
});

describe('primaryDate', () => {
  it('uses deadline as primary with kind-aware verb', () => {
    const d = primaryDate({
      deadline: '2026-07-18T00:00:00Z',
      posted_at: '2026-07-01T00:00:00Z',
      kind: 'scholarship',
      now: NOW,
    });
    expect(d.source).toBe('deadline');
    expect(d.urgency).toBe('urgent');
    expect(d.verb).toBe('Apply by');
    expect(d.shortLabel).toBe('3d left');
    expect(d.label).toContain('Apply by');
    expect(d.label).toContain('in 3 days');
  });

  it('uses Closes for tenders and Expires for deals', () => {
    expect(primaryDate({ deadline: '2026-07-20T00:00:00Z', kind: 'tender', now: NOW }).verb).toBe(
      'Closes'
    );
    expect(primaryDate({ deadline: '2026-07-20T00:00:00Z', kind: 'deal', now: NOW }).verb).toBe(
      'Expires'
    );
  });

  it('falls back to posted_at when no deadline', () => {
    const d = primaryDate({
      posted_at: '2026-07-14T00:00:00Z',
      kind: 'job',
      now: NOW,
    });
    expect(d.source).toBe('posted');
    expect(d.label).toMatch(/Posted/i);
    expect(d.shortLabel).toBeTruthy();
  });

  it('returns empty when no dates at all', () => {
    const d = primaryDate({ kind: 'job', now: NOW });
    expect(d.source).toBe('none');
    expect(d.label).toBe('');
  });
});
