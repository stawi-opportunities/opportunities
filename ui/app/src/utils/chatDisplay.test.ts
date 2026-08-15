import { describe, expect, it } from 'vitest';
import {
  displayUserContent,
  filterPlacementMessages,
  isOpportunityThreadNoise,
  stripViewingChrome,
} from './chatDisplay';

describe('chatDisplay', () => {
  it('strips viewing opportunity chrome', () => {
    const raw =
      '[Viewing opportunity: "Time Out South Africa | Multimedia Producer" at Kagiso Media, SA. slug=time-out]\n\nDo I have a valid resume?';
    expect(stripViewingChrome(raw)).toBe('Do I have a valid resume?');
    expect(displayUserContent(raw)).toBe('Do I have a valid resume?');
  });

  it('filters opportunity noise from placement transcript', () => {
    const msgs = [
      {
        role: 'assistant',
        content:
          "You're viewing Time Out South Africa | Multimedia Producer at Kagiso Media (SA). Ask anything.",
      },
      {
        role: 'user',
        content:
          '[Viewing opportunity: "X" at Y. slug=z]\n\nDo I have a valid resume that can help with personalizing to this job',
      },
      { role: 'assistant', content: 'What role should we match you to?' },
      { role: 'user', content: 'Senior Software Engineer' },
    ];
    expect(isOpportunityThreadNoise(msgs[0]!)).toBe(true);
    const cleaned = filterPlacementMessages(msgs);
    expect(cleaned).toEqual([
      { role: 'assistant', content: 'What role should we match you to?' },
      { role: 'user', content: 'Senior Software Engineer' },
    ]);
  });
});
