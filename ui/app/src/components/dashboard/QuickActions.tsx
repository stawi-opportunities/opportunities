interface Action {
  href: string;
  label: string;
  description: string;
}

const ACTIONS: Action[] = [
  {
    href: '/jobs/',
    label: 'Browse jobs',
    description: 'Search all opportunities',
  },
  {
    href: '/dashboard/#saved',
    label: 'Saved',
    description: 'View bookmarked items',
  },
  {
    href: '/dashboard/#preferences',
    label: 'Preferences',
    description: 'Update match settings',
  },
  {
    href: '/dashboard/#settings',
    label: 'Settings',
    description: 'Profile & account',
  },
];

export function QuickActions() {
  return (
    <div className="flex flex-wrap gap-3">
      {ACTIONS.map((a) => (
        <a
          key={a.href}
          href={a.href}
          className="rounded-lg border border-gray-200 bg-white px-4 py-2.5 text-sm font-medium text-gray-700 transition-colors hover:bg-gray-50 hover:text-gray-900 dark:border-navy-700 dark:bg-navy-900 dark:text-gray-300 dark:hover:bg-navy-800 dark:hover:text-white"
        >
          <span>{a.label}</span>
          <span className="ml-1.5 text-xs text-gray-400 dark:text-gray-500">{a.description}</span>
        </a>
      ))}
    </div>
  );
}
