import { useEffect, useRef } from 'react';
import { mountSanitisedHTML } from '@/utils/html';

/**
 * Renders an opportunity description stored as sanitized HTML.
 * Falls back to plain-text for rare legacy rows without tags.
 * Client-side mountSanitisedHTML never executes scripts (DOMParser).
 */
export default function DescriptionBody({
  html,
  ariaLabel,
}: {
  html?: string;
  ariaLabel: string;
}) {
  const ref = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.replaceChildren();
    const body = html?.trim() ?? '';
    if (!body) return;
    // Prefer HTML path when the body looks like markup; else text.
    if (body.includes('<')) {
      mountSanitisedHTML(el, body);
    } else {
      el.textContent = body;
    }
  }, [html]);

  return (
    <section
      ref={ref}
      className="prose prose-slate mt-8 max-w-none"
      aria-label={ariaLabel}
    />
  );
}
