import { useState } from 'react';
import { fitJob, type JobFitResult } from '@/api/tools';
import { useAuth } from '@/providers/AuthProvider';
import { Button } from '@/components/ui/Button';
import { useToast } from '@/hooks/useToast';

/**
 * Job-page fitness checker. Uses stored CV + opportunity_id when available.
 */
export function JobFitPanel({
  opportunityId,
  title,
  description,
}: {
  opportunityId: string;
  title?: string;
  description?: string;
}) {
  const { hasSession, ready, login } = useAuth();
  const { push: toast } = useToast();
  const [fit, setFit] = useState<JobFitResult | null>(null);
  const [loading, setLoading] = useState(false);

  if (!ready) return null;

  if (!hasSession) {
    return (
      <section className="mt-10 rounded-lg border border-muted bg-surface-muted p-4">
        <h2 className="text-base font-semibold text-main">Job fitness</h2>
        <p className="mt-1 text-sm text-secondary">
          Sign in with a CV on file to see how well this role matches you.
        </p>
        <Button
          className="mt-3"
          size="sm"
          variant="secondary"
          type="button"
          onClick={() => void login()}
        >
          Sign in
        </Button>
      </section>
    );
  }

  async function run() {
    setLoading(true);
    setFit(null);
    try {
      const res = await fitJob({
        opportunity_id: opportunityId || undefined,
        title: title || undefined,
        // Fallback when id is missing but we have listing text.
        job_text: !opportunityId && description ? description : undefined,
      });
      setFit(res);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      if (/cv|embedding|profile/i.test(msg)) {
        toast('Upload a CV under Dashboard → CV to check fit.', 'error');
      } else {
        toast('Could not score job fit. Try again.', 'error');
      }
    } finally {
      setLoading(false);
    }
  }

  return (
    <section className="mt-10 rounded-lg border border-muted p-4 sm:p-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-base font-semibold text-main">How fit am I?</h2>
          <p className="mt-1 text-sm text-secondary">
            Score this role against your CV — semantic + keyword signals.
          </p>
        </div>
        <Button
          type="button"
          variant="primary"
          size="sm"
          disabled={loading}
          onClick={() => void run()}
        >
          {loading ? 'Checking…' : fit ? 'Re-check fit' : 'Check fit'}
        </Button>
      </div>

      {fit && (
        <div className="mt-5 space-y-3">
          <div className="flex flex-wrap items-end gap-3">
            <span className="text-4xl font-bold text-main">{fit.score}</span>
            <span className="pb-1 text-sm capitalize text-secondary">/ 100 · {fit.label} fit</span>
            {fit.method && (
              <span className="mb-1 rounded-full bg-surface-muted px-2 py-0.5 text-[10px] font-medium uppercase tracking-wide text-secondary">
                {fit.method.startsWith('vector') ? 'AI + keywords' : 'Keywords only'}
              </span>
            )}
          </div>
          {(fit.vector_score != null || fit.keyword_score != null) && (
            <div className="flex flex-wrap gap-3 text-xs text-secondary">
              {fit.vector_score != null && (
                <span>
                  Semantic: <strong className="text-main">{fit.vector_score}</strong>
                </span>
              )}
              {fit.keyword_score != null && (
                <span>
                  Keywords: <strong className="text-main">{fit.keyword_score}</strong>
                </span>
              )}
            </div>
          )}
          {fit.signals.length > 0 && (
            <ul className="list-disc space-y-1 pl-5 text-sm text-main">
              {fit.signals.map((s) => (
                <li key={s}>{s}</li>
              ))}
            </ul>
          )}
          {fit.suggestions.length > 0 && (
            <div>
              <p className="text-xs font-semibold uppercase tracking-wide text-secondary">
                Suggestions
              </p>
              <ul className="mt-1 list-disc space-y-1 pl-5 text-sm text-main">
                {fit.suggestions.map((s) => (
                  <li key={s}>{s}</li>
                ))}
              </ul>
            </div>
          )}
        </div>
      )}
    </section>
  );
}
