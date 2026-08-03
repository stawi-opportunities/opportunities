/**
 * Build a printable HTML document from CV plain text + optional meta.
 * PDF v1 = open HTML and use browser print / save as PDF.
 */

export interface CVExportInput {
  title?: string;
  candidateName?: string;
  targetRole?: string;
  bodyText: string;
  generatedAt?: Date;
}

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

export function buildCVHtmlDocument(input: CVExportInput): string {
  const when = (input.generatedAt ?? new Date()).toISOString().slice(0, 10);
  const name = escapeHtml(input.candidateName?.trim() || 'Curriculum Vitae');
  const role = input.targetRole?.trim()
    ? `<p class="role">${escapeHtml(input.targetRole.trim())}</p>`
    : '';
  const title = escapeHtml(input.title?.trim() || name);

  return `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>${title}</title>
  <style>
    :root { color-scheme: light; }
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
    <p class="meta">Exported ${when} · Stawi</p>
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
