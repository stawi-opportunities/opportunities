import { useEffect, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { statsSummary } from '@/api/search';
import type { StatsSummary } from '@/types/search';
import { useI18n } from '@/i18n/I18nProvider';

/**
 * Homepage stats band — proof of scale for anonymous visitors before
 * sign-in. Backed by the public GET /api/stats (Cache-Control:
 * public, max-age=300), so it is cheap and safe to show above the
 * fold. Counts animate once on first reveal; reduced-motion users get
 * the final value immediately.
 */

const ROW_HEIGHT = '2.75rem';

function useCountUp(target: number): number {
  const [value, setValue] = useState(0);
  const ref = useRef<number | null>(null);

  useEffect(() => {
    const reduced =
      typeof window !== 'undefined' &&
      window.matchMedia('(prefers-reduced-motion: reduce)').matches;
    if (reduced || target === 0) {
      setValue(target);
      return;
    }
    let raf = 0;
    const start = performance.now();
    const duration = 900;
    const tick = (now: number) => {
      const p = Math.min(1, (now - start) / duration);
      // easeOutCubic
      const eased = 1 - Math.pow(1 - p, 3);
      setValue(Math.round(eased * target));
      if (p < 1) raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);
    return () => {
      cancelAnimationFrame(raf);
      ref.current = null;
    };
  }, [target]);

  return value;
}

function StatCell({ value, label }: { value: number; label: string }) {
  const shown = useCountUp(value);
  return (
    <div className="flex flex-col items-center gap-1 px-4">
      <span
        className="font-display text-3xl font-semibold tabular-nums tracking-tight text-main sm:text-4xl"
        style={{ lineHeight: 1 }}
      >
        {shown.toLocaleString()}
      </span>
      <span className="text-center text-xs text-secondary sm:text-sm">{label}</span>
    </div>
  );
}

export default function HomeStatsBand() {
  const { t } = useI18n();
  const q = useQuery({
    queryKey: ['home-stats'],
    queryFn: statsSummary,
    staleTime: 5 * 60_000,
    retry: 1,
  });

  const stats: StatsSummary | null = q.data ?? null;

  // Hide silently when loading, erroring, or everything is zero (empty
  // backend) — the band is enhancement, never a broken block.
  if (q.isLoading || !stats) return null;
  if (
    q.isError ||
    (stats.total_jobs === 0 && stats.total_companies === 0 && stats.countries === 0)
  ) {
    return null;
  }

  return (
    <section
      aria-label={t('home.statsLabel')}
      className="mt-14 w-full border-y border-muted bg-surface/40 py-8"
    >
      <div
        className="mx-auto flex max-w-3xl items-center justify-center gap-2 sm:gap-4"
        style={{ minHeight: ROW_HEIGHT }}
      >
        <StatCell value={stats.total_jobs} label={t('home.statsJobs')} />
        <span aria-hidden="true" className="h-10 w-px bg-muted-strong" />
        <StatCell value={stats.total_companies} label={t('home.statsCompanies')} />
        <span aria-hidden="true" className="h-10 w-px bg-muted-strong" />
        <StatCell value={stats.countries} label={t('home.statsCountries')} />
      </div>
    </section>
  );
}
