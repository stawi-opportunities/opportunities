import { describe, expect, it } from 'vitest';
import { buildCVHtmlDocument } from './cvExport';

describe('buildCVHtmlDocument', () => {
  it('escapes HTML and includes body paragraphs', () => {
    const html = buildCVHtmlDocument({
      candidateName: 'Ada <Lovelace>',
      targetRole: 'Engineer',
      bodyText: 'Line one\n\nLine two & three',
      generatedAt: new Date('2026-08-03T00:00:00Z'),
    });
    expect(html).toContain('Ada &lt;Lovelace&gt;');
    expect(html).toContain('Engineer');
    expect(html).toContain('Line one');
    expect(html).toContain('Line two &amp; three');
    expect(html).toContain('2026-08-03');
    expect(html).not.toContain('<script>');
  });
});
