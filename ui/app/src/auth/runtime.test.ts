import { describe, expect, it } from 'vitest';
import { opportunitiesAuthScopes } from './runtime';

describe('opportunities auth runtime scopes', () => {
  it('requests only scopes allowed by the OAuth client', () => {
    expect(opportunitiesAuthScopes).toEqual(['openid', 'profile', 'offline_access']);
  });
});
