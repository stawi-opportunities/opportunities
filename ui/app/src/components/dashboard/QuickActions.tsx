import { useI18n } from '@/i18n/I18nProvider';

interface Action {
  href: string;
  label: string;
  description: string;
}

export function QuickActions() {
  const { t } = useI18n();
  const actions: Action[] = [
    {
      href: '/jobs/',
      label: t('dash.browseJobs'),
      description: t('dash.qaBrowseDesc'),
    },
    {
      href: '/dashboard/#saved',
      label: t('nav.saved'),
      description: t('dash.qaSavedDesc'),
    },
    {
      href: '/dashboard/#preferences',
      label: t('nav.preferences'),
      description: t('dash.qaPrefsDesc'),
    },
    {
      href: '/dashboard/#settings',
      label: t('nav.settings'),
      description: t('dash.qaSettingsDesc'),
    },
  ];

  return (
    <div className="flex flex-wrap gap-3">
      {actions.map((a) => (
        <a
          key={a.href}
          href={a.href}
          className="rounded-xl border-0 bg-white px-4 py-2.5 text-sm font-medium text-gray-700 shadow-sm ring-1 ring-gray-200 transition-all hover:-translate-y-0.5 hover:shadow-md hover:text-gray-900 dark:bg-navy-900 dark:text-gray-300 dark:ring-navy-700 dark:hover:bg-navy-800 dark:hover:text-white"
        >
          <span>{a.label}</span>
          <span className="ml-1.5 text-xs text-gray-400 dark:text-gray-500">{a.description}</span>
        </a>
      ))}
    </div>
  );
}
