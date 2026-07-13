import { useEffect, useState } from 'react';
import { fetchOpportunities } from '@/api/candidates';
import { StatCard } from './StatCard';

interface Stats {
  total: number;
  matches: number;
  starred: number;
  applied: number;
}

export function StatsRow() {
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

  const cards = [
    {
      label: 'Total',
      value: stats?.total ?? 0,
      href: '/dashboard/#feed',
      icon: 'briefcase' as const,
      color: 'blue' as const,
    },
    {
      label: 'Matches',
      value: stats?.matches ?? 0,
      href: '/dashboard/#feed?filter=matches',
      icon: 'heart' as const,
      color: 'teal' as const,
    },
    {
      label: 'Saved',
      value: stats?.starred ?? 0,
      href: '/dashboard/#saved',
      icon: 'star' as const,
      color: 'purple' as const,
    },
    {
      label: 'Applied',
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
