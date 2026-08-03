import { useCallback, useEffect, useRef, useState } from 'react';
import { fetchMeCV, uploadCV, type MeCVDocument } from '@/api/profile';
import { scoreCV, type CVStrengthReport } from '@/api/tools';
import { useCandidateProfile } from '@/hooks/useCandidateProfile';
import { useToast } from '@/hooks/useToast';
import { Button } from '@/components/ui/Button';
import { Panel } from './Panel';
import { PreferencesPanel } from './PreferencesPanel';
import { buildCVHtmlDocument, downloadCVHtml, openCVPrintWindow } from '@/utils/cvExport';

/**
 * CV hub: document, ATS score + rewrite diffs, export, match preferences.
 * Replaces the old Tools + Preferences top-level sections.
 */
export function CVPanel() {
  const { push: toast } = useToast();
  const profileQ = useCandidateProfile();
  const fileRef = useRef<HTMLInputElement>(null);

  const [cvDoc, setCvDoc] = useState<MeCVDocument | null>(null);
  const [cvLoading, setCvLoading] = useState(true);
  const [uploading, setUploading] = useState(false);

  const [targetRole, setTargetRole] = useState('');
  const [cvPaste, setCvPaste] = useState('');
  const [report, setReport] = useState<CVStrengthReport | null>(null);
  const [scoring, setScoring] = useState(false);

  const reloadCV = useCallback(async () => {
    setCvLoading(true);
    try {
      const doc = await fetchMeCV();
      setCvDoc(doc);
      setCvPaste((prev) => {
        if (prev.trim()) return prev;
        if (doc?.extracted_text) return doc.extracted_text.slice(0, 50_000);
        return prev;
      });
    } finally {
      setCvLoading(false);
    }
  }, []);

  useEffect(() => {
    void reloadCV();
  }, [reloadCV]);

  async function onUpload(file: File) {
    setUploading(true);
    try {
      const res = await uploadCV(file);
      toast('CV uploaded. Matching will use the new version shortly.', 'success');
      if (res.extracted_text) setCvPaste(res.extracted_text.slice(0, 50_000));
      await reloadCV();
      setReport(null);
    } catch (err) {
      toast(err instanceof Error ? err.message : 'Could not upload CV.', 'error');
    } finally {
      setUploading(false);
    }
  }

  async function runScore() {
    setScoring(true);
    setReport(null);
    try {
      const res = await scoreCV({
        target_role: targetRole.trim() || undefined,
        cv_text: cvPaste.trim() || undefined,
      });
      setReport(res);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      if (/cv_text_required/i.test(msg)) {
        toast('Upload a CV or paste resume text below.', 'error');
      } else if (/scorer_unavailable/i.test(msg)) {
        toast('CV scoring is temporarily unavailable.', 'error');
      } else {
        toast('Could not score CV. Try again.', 'error');
      }
    } finally {
      setScoring(false);
    }
  }

  function exportBody(): string {
    return cvPaste.trim() || cvDoc?.extracted_text?.trim() || profileQ.data?.current_title || '';
  }

  function handleExportHtml() {
    const body = exportBody();
    if (!body) {
      toast('Upload or paste CV text before exporting.', 'error');
      return;
    }
    const html = buildCVHtmlDocument({
      candidateName: profileQ.data?.current_title || undefined,
      targetRole: targetRole || report?.target_role,
      bodyText: body,
    });
    downloadCVHtml('stawi-cv.html', html);
    toast('HTML CV downloaded.', 'success');
  }

  function handleExportPdf() {
    const body = exportBody();
    if (!body) {
      toast('Upload or paste CV text before exporting.', 'error');
      return;
    }
    const html = buildCVHtmlDocument({
      candidateName: profileQ.data?.current_title || undefined,
      targetRole: targetRole || report?.target_role,
      bodyText: body,
    });
    openCVPrintWindow(html);
  }

  const present = Boolean(cvDoc?.present || cvDoc?.extracted_text || cvPaste.trim());

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-lg font-semibold text-main">Your CV</h2>
        <p className="mt-1 text-sm text-secondary">
          Keep one living CV for matching, ATS checks, and clean exports. Preferences below tell us
          which roles to score and surface.
        </p>
      </div>

      <Panel title="Document">
        {cvLoading ? (
          <p className="text-sm text-secondary">Loading CV…</p>
        ) : (
          <div className="space-y-3 text-sm">
            <p className="text-secondary">
              {present ? (
                <>
                  <span className="font-medium text-emerald-700 dark:text-emerald-400">
                    CV on file
                  </span>
                  {cvDoc?.cv_version != null && (
                    <span className="text-secondary"> · version {cvDoc.cv_version}</span>
                  )}
                  {cvDoc?.cv_length != null && cvDoc.cv_length > 0 && (
                    <span className="text-secondary">
                      {' '}
                      · {cvDoc.cv_length.toLocaleString()} chars
                    </span>
                  )}
                  {cvDoc?.placement_ready === false && (
                    <span className="mt-1 block text-amber-700 dark:text-amber-300">
                      Profile not fully ready for matching — complete preferences below.
                    </span>
                  )}
                </>
              ) : (
                <span className="text-amber-700 dark:text-amber-300">
                  No CV uploaded yet. Upload a PDF or Word file to unlock scored matches.
                </span>
              )}
            </p>
            <div className="flex flex-wrap gap-2">
              <input
                ref={fileRef}
                type="file"
                accept=".pdf,.doc,.docx,.txt,application/pdf"
                className="hidden"
                onChange={(e) => {
                  const f = e.target.files?.[0];
                  if (f) void onUpload(f);
                  e.target.value = '';
                }}
              />
              <Button
                type="button"
                variant="primary"
                size="sm"
                disabled={uploading}
                onClick={() => fileRef.current?.click()}
              >
                {uploading ? 'Uploading…' : present ? 'Replace CV' : 'Upload CV'}
              </Button>
            </div>
          </div>
        )}
      </Panel>

      <Panel title="ATS score & improvements">
        <p className="text-sm text-secondary">
          LLM-assisted score for parseability, keywords, impact, role fit, and clarity — with a
          concrete diff of what to change.
        </p>
        <div className="mt-4 grid gap-3 sm:grid-cols-2">
          <label className="block text-sm">
            <span className="font-medium text-main">Target role (optional)</span>
            <input
              value={targetRole}
              onChange={(e) => setTargetRole(e.target.value)}
              placeholder="e.g. Backend Engineer"
              className="mt-1 w-full rounded-md border border-muted bg-surface px-3 py-2 text-sm text-main"
            />
          </label>
        </div>
        <label className="mt-3 block text-sm">
          <span className="font-medium text-main">
            CV text {present ? '(optional override)' : '(required if no upload)'}
          </span>
          <textarea
            value={cvPaste}
            onChange={(e) => setCvPaste(e.target.value)}
            rows={6}
            placeholder="Paste resume text to score or edit before re-scoring…"
            className="mt-1 w-full rounded-md border border-muted bg-surface px-3 py-2 font-mono text-xs text-main"
          />
        </label>
        <div className="mt-3 flex flex-wrap gap-2">
          <Button
            type="button"
            variant="primary"
            disabled={scoring}
            onClick={() => void runScore()}
          >
            {scoring ? 'Scoring…' : report ? 'Re-score CV' : 'Score my CV'}
          </Button>
        </div>

        {report && (
          <div className="mt-6 space-y-4">
            <div className="flex items-end gap-3">
              <span className="text-4xl font-bold text-main">{report.overall_score}</span>
              <span className="pb-1 text-sm text-secondary">/ 100 overall</span>
              {report.target_role && (
                <span className="pb-1 text-xs text-secondary">· {report.target_role}</span>
              )}
            </div>
            <div className="grid grid-cols-2 gap-2 sm:grid-cols-5">
              {(
                [
                  ['ATS', report.components.ats],
                  ['Keywords', report.components.keywords],
                  ['Impact', report.components.impact],
                  ['Role fit', report.components.role_fit],
                  ['Clarity', report.components.clarity],
                ] as const
              ).map(([label, n]) => (
                <div key={label} className="rounded-lg border border-muted p-3 text-center">
                  <p className="text-xs uppercase tracking-wide text-secondary">{label}</p>
                  <p className="mt-1 text-lg font-semibold text-main">{n}</p>
                </div>
              ))}
            </div>

            {report.priority_fixes?.length > 0 && (
              <div>
                <h3 className="text-sm font-semibold text-main">Priority improvements</h3>
                <ul className="mt-2 space-y-2">
                  {report.priority_fixes.slice(0, 8).map((f) => (
                    <li
                      key={f.id}
                      className="rounded-md border border-muted bg-surface-muted p-3 text-sm"
                    >
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="font-medium text-main">{f.title}</span>
                        <span className="rounded-full bg-surface px-2 py-0.5 text-[10px] uppercase text-secondary ring-1 ring-muted">
                          {f.impact} · {f.category}
                          {f.auto_applicable ? ' · auto' : ''}
                        </span>
                      </div>
                      <p className="mt-1 text-secondary">{f.why}</p>
                      {f.suggestions && f.suggestions.length > 0 && (
                        <ul className="mt-1 list-disc pl-5 text-xs text-secondary">
                          {f.suggestions.map((s) => (
                            <li key={s}>{s}</li>
                          ))}
                        </ul>
                      )}
                    </li>
                  ))}
                </ul>
              </div>
            )}

            {report.rewrites && report.rewrites.length > 0 && (
              <div>
                <h3 className="text-sm font-semibold text-main">Suggested rewrites</h3>
                <ul className="mt-2 space-y-3">
                  {report.rewrites.slice(0, 6).map((r, i) => (
                    <li key={i} className="overflow-hidden rounded-lg border border-muted text-sm">
                      <div className="grid gap-0 sm:grid-cols-2">
                        <div className="border-b border-muted bg-red-50/80 p-3 dark:border-navy-700 dark:bg-red-950/20 sm:border-b-0 sm:border-r">
                          <p className="text-[10px] font-semibold uppercase tracking-wide text-red-800 dark:text-red-300">
                            Before
                          </p>
                          <p className="mt-1 whitespace-pre-wrap text-secondary">{r.before}</p>
                        </div>
                        <div className="bg-emerald-50/80 p-3 dark:bg-emerald-950/20">
                          <p className="text-[10px] font-semibold uppercase tracking-wide text-emerald-800 dark:text-emerald-300">
                            After
                          </p>
                          <p className="mt-1 whitespace-pre-wrap text-main">{r.after}</p>
                        </div>
                      </div>
                      {r.reason && (
                        <p className="border-t border-muted bg-surface-muted px-3 py-2 text-xs text-secondary">
                          {r.reason}
                        </p>
                      )}
                    </li>
                  ))}
                </ul>
                <p className="mt-2 text-xs text-secondary">
                  Copy improved lines into your CV file, re-upload, then re-score to track progress.
                </p>
              </div>
            )}
          </div>
        )}
      </Panel>

      <Panel title="Export">
        <p className="text-sm text-secondary">
          Download a clean, templated CV for applications outside Stawi. PDF uses your browser’s
          print dialog (Save as PDF).
        </p>
        <div className="mt-3 flex flex-wrap gap-2">
          <Button type="button" variant="primary" size="sm" onClick={handleExportHtml}>
            Download HTML
          </Button>
          <Button type="button" variant="secondary" size="sm" onClick={handleExportPdf}>
            Print / Save PDF
          </Button>
        </div>
      </Panel>

      <PreferencesPanel />
    </div>
  );
}
