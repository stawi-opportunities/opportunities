import { useEffect, useState } from 'react';
import { fetchOpportunities } from '@/api/candidates';

interface Stats {
  total: number;
  matches: number;
  starred: number;
  applied: number;
}

export function OverviewPanel() {
  const [stats, setStats] = useState<Stats | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let mounted = true;
    (async () => {
      try {
        const page = await fetchOpportunities();
        if (!mounted) return;
        const items = page.items;
        setStats({
          total: items.length,
          matches: items.filter((i) => (i.score ?? 0) > 0).length,
          starred: items.filter((i) => i.starred).length,
          applied: items.filter((i) => i.application).length,
        });
      } catch {
        if (!mounted) return;
        setStats(null);
      } finally {
        if (mounted) setLoading(false);
      }
    })();
    return () => {
      mounted = false;
    };
  }, []);

  if (loading) {
    return (
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {[1, 2, 3, 4].map((i) => (
          <div
            key={i}
            className="animate-pulse rounded-lg border border-gray-200 bg-white p-5 dark:border-navy-700 dark:bg-navy-900"
          >
            <div className="h-3 w-20 rounded bg-gray-100 dark:bg-navy-800" />
            <div className="mt-2 h-8 w-12 rounded bg-gray-100 dark:bg-navy-800" />
          </div>
        ))}
      </div>
    );
  }

  const cards = [
    { label: 'Total', value: stats?.total ?? 0, href: '/dashboard/#feed' },
    { label: 'Matches', value: stats?.matches ?? 0, href: '/dashboard/#feed?filter=matches' },
    { label: 'Saved', value: stats?.starred ?? 0, href: '/dashboard/#saved' },
    { label: 'Applied', value: stats?.applied ?? 0, href: '/dashboard/#applications' },
  ];

  return (
    <div className="space-y-6">
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {cards.map((c) => (
          <a
            key={c.label}
            href={c.href}
            className="block rounded-lg border border-gray-200 bg-white p-5 transition-colors hover:bg-gray-50 dark:border-navy-700 dark:bg-navy-900 dark:hover:bg-navy-800"
          >
            <p className="text-xs font-medium uppercase tracking-wide text-gray-500 dark:text-gray-400">
              {c.label}
            </p>
            <p className="mt-1 text-3xl font-bold text-gray-900 dark:text-white">{c.value}</p>
          </a>
        ))}
      </div>

      <div className="rounded-lg border border-gray-200 bg-white p-6 dark:border-navy-700 dark:bg-navy-900">
        <h2 className="text-lg font-semibold text-gray-900 dark:text-white">Getting started</h2>
        <ul className="mt-4 space-y-3">
          <li className="flex items-start gap-3 text-sm text-gray-600 dark:text-gray-300">
            <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-accent-100 text-xs font-bold text-accent-700 dark:bg-accent-900/30 dark:text-accent-300">
              1
            </span>
            <span>
              Complete your{' '}
              <a
                href="/dashboard/#settings"
                className="font-medium text-accent-600 hover:text-accent-700 dark:text-accent-400"
              >
                profile
              </a>{' '}
              to improve your matches.
            </span>
          </li>
          <li className="flex items-start gap-3 text-sm text-gray-600 dark:text-gray-300">
            <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-accent-100 text-xs font-bold text-accent-700 dark:bg-accent-900/30 dark:text-accent-300">
              2
            </span>
            <span>
              Set your{' '}
              <a
                href="/dashboard/#preferences"
                className="font-medium text-accent-600 hover:text-accent-700 dark:text-accent-400"
              >
                preferences
              </a>{' '}
              so we match you with the right opportunities.
            </span>
          </li>
          <li className="flex items-start gap-3 text-sm text-gray-600 dark:text-gray-300">
            <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-accent-100 text-xs font-bold text-accent-700 dark:bg-accent-900/30 dark:text-accent-300">
              3
            </span>
            <span>
              <a
                href="/dashboard/#feed"
                className="font-medium text-accent-600 hover:text-accent-700 dark:text-accent-400"
              >
                Browse
              </a>{' '}
              your feed and save or apply to opportunities.
            </span>
          </li>
        </ul>
      </div>
    </div>
  );
}
