import { describe, it, expect } from 'vitest';
import { chatErrorMessage } from './candidates';

describe('chatErrorMessage', () => {
  it('extracts problem+json detail from AuthError-style message', () => {
    const msg = chatErrorMessage(
      new Error(
        'API 502: {"title":"chat_agent_turn_failed","detail":"I couldn\'t process that message with the assistant just now. Nothing was saved from this turn — please try again.","status":502}'
      )
    );
    expect(msg).toMatch(/couldn't process that message/i);
    expect(msg).toMatch(/nothing was saved/i);
  });

  it('maps timeout codes', () => {
    expect(chatErrorMessage({ code: 'NETWORK_TIMEOUT', message: 'timeout' })).toMatch(/too long/i);
  });

  it('maps unauthorized', () => {
    expect(chatErrorMessage({ code: 'API_UNAUTHORIZED', message: 'API 401: x' })).toMatch(
      /sign in/i
    );
  });
});
