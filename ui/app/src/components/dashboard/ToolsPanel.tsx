import { useState } from 'react';
import { scoreCV, fitJob, type CVStrengthReport, type JobFitResult } from '@/api/tools';
import { Panel } from './Panel';
import { Button } from '@/components/ui/Button';
import { useToast } from '@/hooks/useToast';

/**
 * Free tools — value before pay: CV ATS score + job fitness checker.
 */
export function ToolsPanel() {
  const { push: toast } = useToast();
  const [targetRole, setTargetRole] = useState('');
  const [cvPaste, setCvPaste] = useState('');
  const [report, setReport] = useState<CVStrengthReport | null>(null);
  const [scoring, setScoring] = useState(false);

  const [jobText, setJobText] = useState('');
  const [jobTitle, setJobTitle] = useState('');
  const [fit, setFit] = useState<JobFitResult | null>(null);
  const [fitting, setFitting] = useState(false);

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
        toast('Upload a CV in Preferences or paste resume text below.', 'error');
      } else if (/scorer_unavailable/i.test(msg)) {
        toast('CV scoring is temporarily unavailable.', 'error');
      } else {
        toast('Could not score CV. Try again.', 'error');
      }
    } finally {
      setScoring(false);
    }
  }

  async function runFit() {
    setFitting(true);
    setFit(null);
    try {
      const res = await fitJob({
        job_text: jobText.trim(),
        title: jobTitle.trim() || undefined,
      });
      setFit(res);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      if (/job_text_required/i.test(msg)) {
        toast('Paste a job description (at least a short paragraph).', 'error');
      } else {
        toast('Could not score job fit. Try again.', 'error');
      }
    } finally {
      setFitting(false);
    }
  }

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-lg font-semibold text-gray-900 dark:text-white">Career tools</h2>
        <p className="mt-1 text-sm text-gray-600 dark:text-gray-400">
          Free for signed-in users. Improve your CV and check fit before you apply — no subscription
          required.
        </p>
      </div>

      <Panel title="CV ATS score">
        <p className="text-sm text-gray-600 dark:text-gray-400">
          Scores ATS parseability, keywords, impact, role fit, and clarity. Uses your uploaded CV
          when available, or paste text below.
        </p>
        <div className="mt-4 grid gap-3 sm:grid-cols-2">
          <label className="block text-sm">
            <span className="font-medium text-gray-700 dark:text-gray-300">
              Target role (optional)
            </span>
            <input
              value={targetRole}
              onChange={(e) => setTargetRole(e.target.value)}
              placeholder="e.g. Backend Engineer"
              className="mt-1 w-full rounded-md border border-gray-300 px-3 py-2 text-sm dark:border-navy-600 dark:bg-navy-800"
            />
          </label>
        </div>
        <label className="mt-3 block text-sm">
          <span className="font-medium text-gray-700 dark:text-gray-300">
            Paste CV text (optional if you already uploaded)
          </span>
          <textarea
            value={cvPaste}
            onChange={(e) => setCvPaste(e.target.value)}
            rows={5}
            placeholder="Paste resume text…"
            className="mt-1 w-full rounded-md border border-gray-300 px-3 py-2 font-mono text-xs dark:border-navy-600 dark:bg-navy-800"
          />
        </label>
        <div className="mt-3">
          <Button
            type="button"
            variant="primary"
            disabled={scoring}
            onClick={() => void runScore()}
          >
            {scoring ? 'Scoring…' : 'Score my CV'}
          </Button>
        </div>

        {report && (
          <div className="mt-6 space-y-4">
            <div className="flex items-end gap-3">
              <span className="text-4xl font-bold text-navy-900 dark:text-white">
                {report.overall_score}
              </span>
              <span className="pb-1 text-sm text-gray-500">/ 100 overall</span>
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
                <div
                  key={label}
                  className="rounded-lg border border-gray-200 p-3 text-center dark:border-navy-700"
                >
                  <p className="text-xs uppercase tracking-wide text-gray-500">{label}</p>
                  <p className="mt-1 text-lg font-semibold text-gray-900 dark:text-white">{n}</p>
                </div>
              ))}
            </div>
            {report.priority_fixes?.length > 0 && (
              <div>
                <h3 className="text-sm font-semibold text-gray-900 dark:text-white">
                  Priority improvements
                </h3>
                <ul className="mt-2 space-y-2">
                  {report.priority_fixes.slice(0, 7).map((f) => (
                    <li
                      key={f.id}
                      className="rounded-md border border-gray-200 bg-gray-50 p-3 text-sm dark:border-navy-700 dark:bg-navy-800"
                    >
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="font-medium text-gray-900 dark:text-white">{f.title}</span>
                        <span className="rounded-full bg-white px-2 py-0.5 text-[10px] uppercase text-gray-600 ring-1 ring-gray-200 dark:bg-navy-900 dark:text-gray-300 dark:ring-navy-600">
                          {f.impact} · {f.category}
                        </span>
                      </div>
                      <p className="mt-1 text-gray-600 dark:text-gray-400">{f.why}</p>
                      {f.suggestions && f.suggestions.length > 0 && (
                        <ul className="mt-1 list-disc pl-5 text-xs text-gray-500">
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
          </div>
        )}
      </Panel>

      <Panel title="Job fitness checker">
        <p className="text-sm text-gray-600 dark:text-gray-400">
          Paste a job description to score fit against your profile. Uses AI embeddings when
          available, blended with keyword overlap so you know why. Helps you decide where to invest
          application time.
        </p>
        <label className="mt-3 block text-sm">
          <span className="font-medium text-gray-700 dark:text-gray-300">Job title (optional)</span>
          <input
            value={jobTitle}
            onChange={(e) => setJobTitle(e.target.value)}
            placeholder="e.g. Senior Backend Engineer"
            className="mt-1 w-full rounded-md border border-gray-300 px-3 py-2 text-sm dark:border-navy-600 dark:bg-navy-800"
          />
        </label>
        <label className="mt-3 block text-sm">
          <span className="font-medium text-gray-700 dark:text-gray-300">Job description</span>
          <textarea
            value={jobText}
            onChange={(e) => setJobText(e.target.value)}
            rows={6}
            placeholder="Paste the full job posting…"
            className="mt-1 w-full rounded-md border border-gray-300 px-3 py-2 font-mono text-xs dark:border-navy-600 dark:bg-navy-800"
          />
        </label>
        <div className="mt-3">
          <Button type="button" variant="primary" disabled={fitting} onClick={() => void runFit()}>
            {fitting ? 'Checking…' : 'Check fit'}
          </Button>
        </div>
        {fit && (
          <div className="mt-6 space-y-3">
            <div className="flex flex-wrap items-end gap-3">
              <span className="text-4xl font-bold text-navy-900 dark:text-white">{fit.score}</span>
              <span className="pb-1 text-sm capitalize text-gray-500">/ 100 · {fit.label} fit</span>
              {fit.method && (
                <span className="mb-1 rounded-full bg-gray-100 px-2 py-0.5 text-[10px] font-medium uppercase tracking-wide text-gray-600 dark:bg-navy-800 dark:text-gray-300">
                  {fit.method.startsWith('vector') ? 'AI + keywords' : 'Keywords only'}
                </span>
              )}
            </div>
            {(fit.vector_score != null || fit.keyword_score != null) && (
              <div className="flex flex-wrap gap-3 text-xs text-gray-500 dark:text-gray-400">
                {fit.vector_score != null && (
                  <span>
                    Semantic:{' '}
                    <strong className="text-gray-800 dark:text-gray-200">{fit.vector_score}</strong>
                  </span>
                )}
                {fit.keyword_score != null && (
                  <span>
                    Keywords:{' '}
                    <strong className="text-gray-800 dark:text-gray-200">
                      {fit.keyword_score}
                    </strong>
                  </span>
                )}
              </div>
            )}
            {fit.signals.length > 0 && (
              <ul className="list-disc space-y-1 pl-5 text-sm text-gray-700 dark:text-gray-300">
                {fit.signals.map((s) => (
                  <li key={s}>{s}</li>
                ))}
              </ul>
            )}
            {fit.suggestions.length > 0 && (
              <div>
                <p className="text-xs font-semibold uppercase tracking-wide text-gray-500">
                  Suggestions
                </p>
                <ul className="mt-1 list-disc space-y-1 pl-5 text-sm text-gray-700 dark:text-gray-300">
                  {fit.suggestions.map((s) => (
                    <li key={s}>{s}</li>
                  ))}
                </ul>
              </div>
            )}
          </div>
        )}
      </Panel>
    </div>
  );
}
