/**
 * Normalize match scores for display and ranking.
 * Backend may send 0–1 cosine/similarity or already 0–100.
 */

export function scoreToPercent(score: number | null | undefined): number | null {
  if (score == null || Number.isNaN(score)) return null;
  if (score < 0) return 0;
  if (score <= 1) return Math.round(score * 100);
  if (score <= 100) return Math.round(score);
  return 100;
}

export type FitBand = 'excellent' | 'strong' | 'good' | 'fair' | 'weak';

export function fitBand(percent: number | null): FitBand | null {
  if (percent == null) return null;
  if (percent >= 85) return 'excellent';
  if (percent >= 70) return 'strong';
  if (percent >= 55) return 'good';
  if (percent >= 40) return 'fair';
  return 'weak';
}

/** Short “why this match” line derived from score band (no server reasons yet). */
export function whyMatched(score: number | null | undefined): string | null {
  const p = scoreToPercent(score);
  const band = fitBand(p);
  if (band == null || p == null) return null;
  switch (band) {
    case 'excellent':
      return `${p}% match — excellent fit with your CV and preferences`;
    case 'strong':
      return `${p}% match — strong alignment with your background`;
    case 'good':
      return `${p}% match — good fit; review details before applying`;
    case 'fair':
      return `${p}% match — partial fit; check requirements carefully`;
    case 'weak':
      return `${p}% match — weaker fit; consider improving your CV first`;
  }
}

export function compareScoreDesc(
  a: number | null | undefined,
  b: number | null | undefined
): number {
  const pa = scoreToPercent(a) ?? -1;
  const pb = scoreToPercent(b) ?? -1;
  return pb - pa;
}
