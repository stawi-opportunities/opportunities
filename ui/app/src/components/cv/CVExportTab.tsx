import { useCallback, useEffect, useState } from 'react';
import { fetchProfileFields } from '@/api/profile';
import { Panel } from '@/components/dashboard/Panel';
import { Button } from '@/components/ui/Button';
import { useToast } from '@/hooks/useToast';
import {
  buildCVHtmlDocument,
  CV_TEMPLATES,
  downloadCVHtml,
  openCVPrintWindow,
  type CVTemplateId,
} from '@/utils/cvExport';
import {
  hydrateStructuredCV,
  structuredCVToPlainText,
  type StructuredCV,
} from '@/utils/structuredCV';

/**
 * Export tab — section-aware HTML / print-PDF from structured CV.
 */
export function CVExportTab() {
  const { push: toast } = useToast();
  const [doc, setDoc] = useState<StructuredCV | null>(null);
  const [template, setTemplate] = useState<CVTemplateId>('classic');
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const pf = await fetchProfileFields();
      setDoc(hydrateStructuredCV(pf));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  function ensureBody(): StructuredCV | null {
    if (!doc) return null;
    const text = structuredCVToPlainText(doc);
    if (!text) {
      toast('Add CV details on the Details tab before exporting.', 'error');
      return null;
    }
    return doc;
  }

  function handleHtml() {
    const d = ensureBody();
    if (!d) return;
    const html = buildCVHtmlDocument({
      document: d,
      candidateName: d.basics.name || undefined,
      targetRole: d.basics.headline || undefined,
      template,
    });
    downloadCVHtml(`stawi-cv-${template}.html`, html);
    toast('HTML CV downloaded.', 'success');
  }

  function handlePdf() {
    const d = ensureBody();
    if (!d) return;
    const html = buildCVHtmlDocument({
      document: d,
      candidateName: d.basics.name || undefined,
      targetRole: d.basics.headline || undefined,
      template,
    });
    openCVPrintWindow(html);
  }

  if (loading) {
    return <p className="text-sm text-secondary">Loading export…</p>;
  }

  return (
    <Panel title="Export your CV">
      <p className="text-sm text-secondary">
        Templates render your structured sections (header, experience, education, skills). PDF uses
        your browser’s print dialog (Save as PDF).
      </p>
      <div className="mt-3 flex flex-wrap gap-2">
        {CV_TEMPLATES.map((tpl) => (
          <button
            key={tpl.id}
            type="button"
            onClick={() => setTemplate(tpl.id)}
            className={`rounded-lg border px-3 py-2 text-left text-sm ${
              template === tpl.id
                ? 'border-accent-500 bg-accent-500/10 text-main'
                : 'border-muted text-secondary hover:border-muted-strong'
            }`}
          >
            <span className="font-medium">{tpl.label}</span>
            <span className="mt-0.5 block text-xs opacity-80">{tpl.hint}</span>
          </button>
        ))}
      </div>
      <div className="mt-4 flex flex-wrap gap-2">
        <Button type="button" variant="primary" size="sm" onClick={handleHtml}>
          Download HTML
        </Button>
        <Button type="button" variant="secondary" size="sm" onClick={handlePdf}>
          Print / Save PDF
        </Button>
      </div>
    </Panel>
  );
}
