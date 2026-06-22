import { Panel } from '@/components/dashboard/Panel';
import type { StringKey } from '@/i18n/strings';

export function SettingsTheme({ t }: { t: (k: StringKey, fallback?: string) => string }) {
  const THEMES: { id: string; labelKey: StringKey }[] = [
    { id: 'light', labelKey: 'settings.themeLight' },
    { id: 'dark', labelKey: 'settings.themeDark' },
    { id: 'system', labelKey: 'settings.themeSystem' },
  ];

  return (
    <Panel title={t('settings.theme')}>
      <div className="space-y-3">
        {THEMES.map((th) => (
          <label key={th.id} className="flex items-center gap-3 text-sm text-gray-700">
            <input
              type="radio"
              name="theme"
              value={th.id}
              defaultChecked={th.id === 'light'}
              className="text-accent-600 focus:ring-accent-500"
            />
            {t(th.labelKey)}
          </label>
        ))}
        <p className="pt-2 text-xs text-gray-400">{t('settings.themeComingSoon')}</p>
      </div>
    </Panel>
  );
}
