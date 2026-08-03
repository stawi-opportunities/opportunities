import { describe, expect, it } from 'vitest';
import { compareScoreDesc, scoreToPercent, whyMatched } from './matchScore';

describe('matchScore', () => {
  it('normalizes 0–1 and 0–100 scores', () => {
    expect(scoreToPercent(0.87)).toBe(87);
    expect(scoreToPercent(87)).toBe(87);
    expect(scoreToPercent(null)).toBeNull();
  });

  it('explains why matched', () => {
    expect(whyMatched(0.9)).toMatch(/excellent/i);
    expect(whyMatched(50)).toMatch(/partial|fair/i);
  });

  it('sorts high score first', () => {
    expect(compareScoreDesc(0.2, 0.9)).toBeGreaterThan(0);
    expect(compareScoreDesc(90, 20)).toBeLessThan(0);
  });
});
