import { useEffect, useState, useCallback } from 'react';
import { fetchOpportunities } from '@/api/candidates';
import { useI18n } from '@/i18n/I18nProvider';
import { StatCard } from './StatCard';

interface Stats {
  total: number;
  matches: number;
  starred: number;
  applied: number;
}

export function StatsRow() {
  const { t } = useI18n();
  const [stats, setStats] = useState<Stats | null>(null);
  const [loading, setLoading] = useState(true);
  const [fetchError, setFetchError] = useState(false);

  const loadStats = useCallback(async () => {
    setLoading(true);
    setFetchError(false);
    try {
      const page = await fetchOpportunities();
      const items = page.items;
      setStats({
        total: items.length,
        matches: items.filter((i) => (i.score ?? 0) > 0).length,
        starred: items.filter((i) => i.starred).length,
        applied: items.filter((i) => i.application).length,
      });
    } catch {
      setStats(null);
      setFetchError(true);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    let mounted = true;
    loadStats().finally(() => {
      if (!mounted) return;
    });
    return () => {
      mounted = false;
    };
  }, [loadStats]);

  if (loading) {
    return (
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {[1, 2, 3, 4].map((i) => (
          <div
            key={i}
            className="animate-pulse rounded-xl border-0 bg-white p-5 shadow-sm ring-1 ring-gray-200 dark:bg-navy-900 dark:ring-navy-700"
          >
            <div className="flex items-start gap-4">
              <div className="h-10 w-10 rounded-xl bg-gray-100 dark:bg-navy-800" />
              <div className="space-y-2">
                <div className="h-3 w-16 rounded bg-gray-100 dark:bg-navy-800" />
                <div className="h-8 w-12 rounded bg-gray-100 dark:bg-navy-800" />
              </div>
            </div>
          </div>
        ))}
      </div>
    );
  }

  if (fetchError) {
    return (
      <div className="flex items-center gap-3 rounded-xl border-0 bg-amber-50 p-4 shadow-sm ring-1 ring-amber-200 dark:bg-amber-900/20 dark:ring-amber-800">
        <p className="text-sm text-amber-800 dark:text-amber-200">{t('dash.statsError')}</p>
        <button
          type="button"
          onClick={loadStats}
          className="ml-auto shrink-0 rounded-md bg-amber-100 px-3 py-1.5 text-xs font-medium text-amber-800 transition-colors hover:bg-amber-200 dark:bg-amber-800 dark:text-amber-200 dark:hover:bg-amber-700"
        >
          {t('cta.retry')}
        </button>
      </div>
    );
  }

  const cards = [
    {
      label: t('dash.statTotal'),
      value: stats?.total ?? 0,
      href: '/dashboard/#feed',
      icon: 'briefcase' as const,
      color: 'blue' as const,
    },
    {
      label: t('nav.matches'),
      value: stats?.matches ?? 0,
      href: '/dashboard/#feed?filter=matches',
      icon: 'heart' as const,
      color: 'teal' as const,
    },
    {
      label: t('nav.saved'),
      value: stats?.starred ?? 0,
      href: '/dashboard/#saved',
      icon: 'star' as const,
      color: 'purple' as const,
    },
    {
      label: t('feed.applied'),
      value: stats?.applied ?? 0,
      href: '/dashboard/#applications',
      icon: 'clipboard' as const,
      color: 'emerald' as const,
    },
  ];

  return (
    <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
      {cards.map((c, i) => (
        <StatCard key={c.label} {...c} index={i} />
      ))}
    </div>
  );
}
