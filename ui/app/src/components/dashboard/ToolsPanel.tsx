import { useState } from 'react';
import { fitJob, type JobFitResult } from '@/api/tools';
import { Panel } from './Panel';
import { Button } from '@/components/ui/Button';
import { useToast } from '@/hooks/useToast';

/**
 * Free tools — job fitness checker. Full ATS reports are paid ($2) from CV → Details.
 */
export function ToolsPanel() {
  const { push: toast } = useToast();
  const [jobText, setJobText] = useState('');
  const [jobTitle, setJobTitle] = useState('');
  const [fit, setFit] = useState<JobFitResult | null>(null);
  const [fitting, setFitting] = useState(false);

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
          Check fit against a job description before you apply. For a full ATS score against your
          matched jobs (emailed report), use <strong>CV → Details → Get ATS report · $2</strong>.
        </p>
      </div>

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
            rows={8}
            placeholder="Paste the full job description…"
            className="mt-1 w-full rounded-md border border-gray-300 px-3 py-2 text-sm dark:border-navy-600 dark:bg-navy-800"
          />
        </label>
        <div className="mt-3">
          <Button type="button" variant="primary" disabled={fitting} onClick={() => void runFit()}>
            {fitting ? 'Scoring…' : 'Check job fit'}
          </Button>
        </div>
        {fit && (
          <div className="mt-6 space-y-3">
            <div className="flex items-end gap-3">
              <span className="text-4xl font-bold text-navy-900 dark:text-white">{fit.score}</span>
              <span className="pb-1 text-sm text-gray-500">
                / 100 · {fit.label}
                {fit.method ? ` · ${fit.method}` : ''}
              </span>
            </div>
            {fit.signals?.length > 0 && (
              <ul className="list-disc space-y-1 pl-5 text-sm text-gray-600 dark:text-gray-400">
                {fit.signals.map((s) => (
                  <li key={s}>{s}</li>
                ))}
              </ul>
            )}
            {fit.suggestions?.length > 0 && (
              <div>
                <h3 className="text-sm font-semibold text-gray-900 dark:text-white">Suggestions</h3>
                <ul className="mt-1 list-disc space-y-1 pl-5 text-sm text-gray-600 dark:text-gray-400">
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
