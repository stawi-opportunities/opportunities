import { Icon } from '@/components/ui/Icon';
import type { IconName } from '@/components/ui/Icon';

interface TimelineEntry {
  icon: IconName;
  title: string;
  description: string;
  href?: string;
}

const entries: TimelineEntry[] = [
  {
    icon: 'dashboard',
    title: 'Welcome to Stawi',
    description: 'Your AI-powered job matching dashboard is ready.',
    href: '/dashboard/#feed',
  },
  {
    icon: 'settings',
    title: 'Complete your profile',
    description: 'Add your job title, preferred countries, and languages.',
    href: '/dashboard/#settings',
  },
  {
    icon: 'tag',
    title: 'Set your preferences',
    description: 'Tell us what kind of opportunities you want.',
    href: '/dashboard/#preferences',
  },
  {
    icon: 'heart',
    title: 'Find your first match',
    description: 'Let AI score opportunities against your profile.',
    href: '/dashboard/#matches',
  },
];

export function ActivityTimeline() {
  return (
    <div className="rounded-xl border border-muted bg-surface p-5">
      <h2 className="text-sm font-semibold text-main">Your activity</h2>
      <div className="mt-4">
        {entries.map((entry, i) => {
          const isLast = i === entries.length - 1;
          const Wrapper = entry.href ? 'a' : 'div';
          return (
            <div key={entry.title} className="relative flex gap-4 pb-2">
              <div className="flex flex-col items-center">
                <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-accent-500/10 text-accent-400 ring-4 ring-surface">
                  <Icon name={entry.icon} size={16} />
                </span>
                {!isLast && <div className="mt-1 h-full w-px bg-muted" aria-hidden="true" />}
              </div>
              <Wrapper
                {...(entry.href ? { href: entry.href } : {})}
                className={`min-h-[44px] flex-1 rounded-lg p-3 transition-colors ${
                  entry.href ? 'hover:bg-surface-hover' : ''
                }`}
              >
                <p className="text-sm font-medium text-main">{entry.title}</p>
                <p className="mt-0.5 text-xs text-secondary">{entry.description}</p>
              </Wrapper>
            </div>
          );
        })}
      </div>
    </div>
  );
}
