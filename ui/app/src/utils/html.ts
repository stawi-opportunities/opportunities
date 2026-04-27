/**
 * Parse server-sanitised HTML via DOMParser and append the parsed body
 * children to a target element. Never assigns innerHTML and never
 * executes scripts — DOMParser with "text/html" produces inert nodes.
 *
 * Used for OpportunitySnapshot.description_html which has already passed through
 * bluemonday's UGC policy server-side (pkg/publish/html.go).
 */
export function mountSanitisedHTML(target: Element, html: string | undefined) {
  if (!html) return;
  const doc = new DOMParser().parseFromString(html, "text/html");
  const frag = document.createDocumentFragment();
  for (const child of Array.from(doc.body.childNodes)) frag.appendChild(child);
  target.appendChild(frag);
}
