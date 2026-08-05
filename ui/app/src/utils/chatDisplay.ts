/**
 * Helpers for rendering chat user content cleanly.
 * Model-only prefixes (job-view chrome) must never appear in bubbles.
 */

/** Strip legacy "[Viewing opportunity: …]" prefix from user messages. */
export function stripViewingChrome(content: string): string {
  const s = content.trim();
  if (!s.startsWith('[Viewing opportunity:')) return s;
  const close = s.indexOf(']');
  if (close < 0 || close + 1 >= s.length) return '';
  return s.slice(close + 1).trim();
}

/** True when a stored turn is job side-chat noise (not placement intake). */
export function isOpportunityThreadNoise(msg: { role: string; content: string }): boolean {
  const c = msg.content.trim();
  if (!c) return true;
  if (c.startsWith('[Viewing opportunity:')) return true;
  if (msg.role.toLowerCase() === 'assistant' && c.startsWith("You're viewing ")) return true;
  return false;
}

/** Keep only placement/onboarding turns; drop job side-chat chrome. */
export function filterPlacementMessages<T extends { role: string; content: string }>(
  messages: T[] | null | undefined
): T[] {
  if (!messages?.length) return [];
  const out: T[] = [];
  for (const m of messages) {
    if (isOpportunityThreadNoise(m)) continue;
    if (m.role.toLowerCase() === 'user') {
      const cleaned = stripViewingChrome(m.content);
      if (!cleaned) continue;
      out.push({ ...m, content: cleaned });
      continue;
    }
    out.push(m);
  }
  return out;
}

/** User-facing content for a bubble (strip chrome; collapse empty). */
export function displayUserContent(content: string): string {
  return stripViewingChrome(content);
}
