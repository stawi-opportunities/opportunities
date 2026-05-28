import { useEffect, useState } from 'react';
import { getConfig } from '@/utils/config';
import { useI18n } from '@/i18n/I18nProvider';

interface Stats {
  views_total: number;
  views_24h: number;
  applies_total: number;
  applies_24h: number;
}

export default function StatsLine({ slug }: { slug: string }) {
  const { t } = useI18n();
  const [stats, setStats] = useState<Stats | null>(null);

  useEffect(() => {
    if (!slug) return;
    const ctrl = new AbortController();
    fetch(`${getConfig().apiURL}/opportunities/${encodeURIComponent(slug)}/stats`, {
      signal: ctrl.signal,
    })
      .then((r) => (r.ok ? r.json() : null))
      .then((data: Stats | null) => {
        if (data) setStats(data);
      })
      .catch(() => {
        // Silent — analytics line is non-critical.
      });
    return () => ctrl.abort();
  }, [slug]);

  if (!stats) return null;
  if (stats.views_total === 0 && stats.applies_total === 0) return null;

  return (
    <p className="mt-3 text-xs text-gray-500">
      <span aria-hidden>👁</span> {fmt(stats.views_total)} {t('stats.views')}
      {' • '}
      <span aria-hidden>✉</span> {fmt(stats.applies_total)} {t('stats.applies')}
    </p>
  );
}

function fmt(n: number): string {
  if (n < 1000) return n.toString();
  if (n < 10_000) return `${(n / 1000).toFixed(1)}k`;
  return `${Math.round(n / 1000)}k`;
}
