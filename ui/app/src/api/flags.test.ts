import { describe, expect, it } from 'vitest';
import { flagPath } from './flags';

describe('flagPath', () => {
  it('keeps the gateway route prefix in front of the api route', () => {
    // Gateway strips the first /opportunities; the api route is
    // /opportunities/{slug}/flag.
    expect(flagPath('senior-engineer-acme')).toBe(
      '/opportunities/opportunities/senior-engineer-acme/flag'
    );
  });

  it('encodes the slug', () => {
    expect(flagPath('a/b c')).toBe('/opportunities/opportunities/a%2Fb%20c/flag');
  });
});
