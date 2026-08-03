/**
 * Build a printable HTML document from CV plain text + optional meta.
 * PDF v1 = open HTML and use browser print / save as PDF.
 */

export type CVTemplateId = 'classic' | 'modern' | 'compact';

export interface CVExportInput {
  title?: string;
  candidateName?: string;
  targetRole?: string;
  bodyText: string;
  generatedAt?: Date;
  template?: CVTemplateId;
}

export const CV_TEMPLATES: { id: CVTemplateId; label: string; hint: string }[] = [
  { id: 'classic', label: 'Classic', hint: 'Serif, traditional applications' },
  { id: 'modern', label: 'Modern', hint: 'Sans-serif, clean tech roles' },
  { id: 'compact', label: 'Compact', hint: 'Tighter spacing for longer CVs' },
];

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

/** Paragraphs from blank-line splits; single newlines → <br>. */
function bodyToHtml(text: string): string {
  const blocks = text
    .trim()
    .split(/\n{2,}/)
    .map((b) => b.trim())
    .filter(Boolean);
  if (blocks.length === 0) return '<p></p>';
  return blocks.map((b) => `<p>${escapeHtml(b).replace(/\n/g, '<br />')}</p>`).join('\n');
}

function templateCSS(id: CVTemplateId): string {
  switch (id) {
    case 'modern':
      return `
    body {
      font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
      font-size: 10.5pt;
      line-height: 1.5;
      color: #0f172a;
      max-width: 720px;
      margin: 0 auto;
      padding: 2rem 1.5rem 3rem;
    }
    h1 { font-size: 1.75rem; margin: 0 0 0.25rem; font-weight: 700; letter-spacing: -0.03em; }
    .role { margin: 0 0 0.5rem; color: #0d9488; font-size: 0.95rem; font-weight: 600; }
    .meta { font-size: 0.7rem; color: #64748b; margin-bottom: 1.5rem; border-bottom: 2px solid #0d9488; padding-bottom: 0.75rem; }
    p { margin: 0 0 0.65rem; }
      `;
    case 'compact':
      return `
    body {
      font-family: "Helvetica Neue", Helvetica, Arial, sans-serif;
      font-size: 9.5pt;
      line-height: 1.35;
      color: #111;
      max-width: 680px;
      margin: 0 auto;
      padding: 1.25rem 1rem 2rem;
    }
    h1 { font-size: 1.25rem; margin: 0 0 0.15rem; letter-spacing: -0.02em; }
    .role { margin: 0 0 0.5rem; color: #333; font-size: 0.85rem; }
    .meta { font-size: 0.65rem; color: #666; margin-bottom: 0.75rem; }
    p { margin: 0 0 0.45rem; }
      `;
    default:
      return `
    body {
      font-family: "Iowan Old Style", "Palatino Linotype", Palatino, Georgia, serif;
      font-size: 11pt;
      line-height: 1.45;
      color: #111;
      max-width: 720px;
      margin: 0 auto;
      padding: 2rem 1.5rem 3rem;
    }
    h1 { font-size: 1.6rem; margin: 0 0 0.25rem; letter-spacing: -0.02em; }
    .role { margin: 0 0 1.25rem; color: #444; font-size: 0.95rem; }
    .meta { font-size: 0.75rem; color: #666; margin-bottom: 1.5rem; }
    p { margin: 0 0 0.75rem; white-space: normal; }
      `;
  }
}

export function buildCVHtmlDocument(input: CVExportInput): string {
  const when = (input.generatedAt ?? new Date()).toISOString().slice(0, 10);
  const name = escapeHtml(input.candidateName?.trim() || 'Curriculum Vitae');
  const role = input.targetRole?.trim()
    ? `<p class="role">${escapeHtml(input.targetRole.trim())}</p>`
    : '';
  const title = escapeHtml(input.title?.trim() || name);
  const tpl = input.template ?? 'classic';

  return `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>${title}</title>
  <style>
    :root { color-scheme: light; }
    ${templateCSS(tpl)}
    @media print {
      body { padding: 0; max-width: none; }
      .no-print { display: none !important; }
    }
  </style>
</head>
<body>
  <header>
    <h1>${name}</h1>
    ${role}
    <p class="meta">Exported ${when} · Stawi · ${tpl}</p>
  </header>
  <main>
    ${bodyToHtml(input.bodyText)}
  </main>
  <p class="no-print meta" style="margin-top:2rem">
    Tip: use your browser Print → Save as PDF for a PDF copy.
  </p>
</body>
</html>`;
}

export function downloadCVHtml(filename: string, html: string): void {
  const blob = new Blob([html], { type: 'text/html;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename.endsWith('.html') ? filename : `${filename}.html`;
  a.rel = 'noopener';
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

/** Open a print-friendly window so the user can Save as PDF. */
export function openCVPrintWindow(html: string): void {
  const w = window.open('', '_blank', 'noopener,noreferrer');
  if (!w) return;
  w.document.open();
  w.document.write(html);
  w.document.close();
  // Defer print until layout paints.
  w.onload = () => {
    try {
      w.focus();
      w.print();
    } catch {
      /* user can print manually */
    }
  };
}
