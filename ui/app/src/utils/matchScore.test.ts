import { describe, expect, it } from 'vitest';
import {
  clampMinFitPercent,
  compareScoreDesc,
  DEFAULT_MIN_FIT_PERCENT,
  minFitPercentToScore,
  scoreToPercent,
  whyMatched,
} from './matchScore';

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

  it('clamps min-fit percent to the slider range', () => {
    expect(clampMinFitPercent(50)).toBe(DEFAULT_MIN_FIT_PERCENT);
    expect(clampMinFitPercent(80)).toBe(80);
    expect(clampMinFitPercent(99)).toBe(95);
    expect(minFitPercentToScore(85)).toBeCloseTo(0.85);
  });
});
