import { useEffect, useState } from 'react';
import { Icon } from '@/components/ui/Icon';
import type { IconName } from '@/components/ui/Icon';

type StatColor = 'blue' | 'teal' | 'purple' | 'emerald';

const COLOR_MAPS: Record<StatColor, { circle: string; text: string; hover: string }> = {
  blue: {
    circle: 'bg-blue-100 text-blue-700 dark:bg-blue-900/40 dark:text-blue-300',
    text: 'text-blue-600 dark:text-blue-400',
    hover: 'group-hover:border-accent-500/40',
  },
  teal: {
    circle: 'bg-teal-100 text-teal-700 dark:bg-teal-900/40 dark:text-teal-300',
    text: 'text-teal-600 dark:text-teal-400',
    hover: 'group-hover:border-accent-500/40',
  },
  purple: {
    circle: 'bg-purple-100 text-purple-700 dark:bg-purple-900/40 dark:text-purple-300',
    text: 'text-purple-600 dark:text-purple-400',
    hover: 'group-hover:border-accent-500/40',
  },
  emerald: {
    circle: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300',
    text: 'text-emerald-600 dark:text-emerald-400',
    hover: 'group-hover:border-accent-500/40',
  },
};

export function StatCard({
  icon,
  label,
  value,
  href,
  color = 'blue',
  index = 0,
}: {
  icon: IconName;
  label: string;
  value: number;
  href: string;
  color?: StatColor;
  index?: number;
}) {
  const [prefersReduced, setPrefersReduced] = useState(false);
  const c = COLOR_MAPS[color];

  useEffect(() => {
    const mq = window.matchMedia('(prefers-reduced-motion: reduce)');
    setPrefersReduced(mq.matches);
    const handler = (e: MediaQueryListEvent) => setPrefersReduced(e.matches);
    mq.addEventListener('change', handler);
    return () => mq.removeEventListener('change', handler);
  }, []);

  return (
    <a
      href={href}
      className={`group block rounded-xl border border-muted bg-surface p-5 transition-all duration-200 hover:-translate-y-0.5 hover:border-muted-strong hover:bg-surface-hover hover:shadow-md ${c.hover} ${prefersReduced ? '' : 'animate-fade-up'}`}
      style={prefersReduced ? undefined : { animationDelay: `${index * 80}ms` }}
    >
      <div className="flex items-start gap-4">
        <span
          className={`flex h-10 w-10 shrink-0 items-center justify-center rounded-xl ${c.circle} transition-transform duration-200 group-hover:scale-110`}
        >
          <Icon name={icon} size={20} />
        </span>
        <div className="min-w-0">
          <p className="text-xs font-medium uppercase tracking-wider text-secondary">{label}</p>
          <p className="mt-0.5 text-3xl font-bold tracking-tight text-main">
            {value.toLocaleString()}
          </p>
        </div>
      </div>
    </a>
  );
}
