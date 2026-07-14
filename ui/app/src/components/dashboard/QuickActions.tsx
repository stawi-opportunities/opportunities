import { useI18n } from '@/i18n/I18nProvider';
import { usePreferenceChatOptional } from '@/components/preference-chat';

interface LinkAction {
  kind: 'link';
  href: string;
  label: string;
  description: string;
}

interface ButtonAction {
  kind: 'button';
  id: string;
  label: string;
  description: string;
  onClick: () => void;
}

type Action = LinkAction | ButtonAction;

export function QuickActions() {
  const { t } = useI18n();
  const preferenceChat = usePreferenceChatOptional();

  const actions: Action[] = [
    {
      kind: 'link',
      href: '/jobs/',
      label: t('dash.browseJobs'),
      description: t('dash.qaBrowseDesc'),
    },
    {
      kind: 'link',
      href: '/dashboard/#saved',
      label: t('nav.saved'),
      description: t('dash.qaSavedDesc'),
    },
    preferenceChat
      ? {
          kind: 'button',
          id: 'tweak-prefs',
          label: 'Tweak matching',
          description: 'Chat to update what you want',
          onClick: () => preferenceChat.openRefine(),
        }
      : {
          kind: 'link',
          href: '/dashboard/#preferences',
          label: t('nav.preferences'),
          description: t('dash.qaPrefsDesc'),
        },
    {
      kind: 'link',
      href: '/dashboard/#settings',
      label: t('nav.settings'),
      description: t('dash.qaSettingsDesc'),
    },
  ];

  const chipClass =
    'rounded-xl border-0 bg-white px-4 py-2.5 text-left text-sm font-medium text-gray-700 shadow-sm ring-1 ring-gray-200 transition-all hover:-translate-y-0.5 hover:shadow-md hover:text-gray-900 dark:bg-navy-900 dark:text-gray-300 dark:ring-navy-700 dark:hover:bg-navy-800 dark:hover:text-white';

  return (
    <div className="flex flex-wrap gap-3">
      {actions.map((a) =>
        a.kind === 'link' ? (
          <a key={a.href} href={a.href} className={chipClass}>
            <span>{a.label}</span>
            <span className="ml-1.5 text-xs text-gray-400 dark:text-gray-500">{a.description}</span>
          </a>
        ) : (
          <button key={a.id} type="button" onClick={a.onClick} className={chipClass}>
            <span>{a.label}</span>
            <span className="ml-1.5 text-xs text-gray-400 dark:text-gray-500">{a.description}</span>
          </button>
        )
      )}
    </div>
  );
}
