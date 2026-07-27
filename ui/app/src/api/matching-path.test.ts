import { describe, expect, it } from 'vitest';
import { matchingPath, matchingPathPrefix } from './matching-path';

describe('matchingPathPrefix', () => {
  it('empty on matching subdomain', () => {
    expect(matchingPathPrefix('https://matching.stawi.org')).toBe('');
  });
  it('/matching on gateway host', () => {
    expect(matchingPathPrefix('https://api.stawi.org')).toBe('/matching');
  });
});

describe('matchingPath', () => {
  it('strips embedded /matching for subdomain', () => {
    expect(matchingPath('/matching/me', 'https://matching.stawi.org')).toBe('/me');
  });
  it('keeps /matching for gateway', () => {
    expect(matchingPath('/me', 'https://api.stawi.org')).toBe('/matching/me');
  });
});
