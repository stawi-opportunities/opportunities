import { useTheme } from '@/providers/ThemeProvider';
import { Panel } from '@/components/dashboard/Panel';
import type { StringKey } from '@/i18n/strings';

export function SettingsTheme({ t }: { t: (k: StringKey, fallback?: string) => string }) {
  const { theme, setTheme } = useTheme();

  const THEMES: { id: 'light' | 'dark' | 'system'; labelKey: StringKey }[] = [
    { id: 'light', labelKey: 'settings.themeLight' },
    { id: 'dark', labelKey: 'settings.themeDark' },
    { id: 'system', labelKey: 'settings.themeSystem' },
  ];

  return (
    <Panel title={t('settings.theme')}>
      <div className="space-y-3">
        {THEMES.map((th) => (
          <label key={th.id} className="flex items-center gap-3 text-sm text-secondary">
            <input
              type="radio"
              name="theme"
              value={th.id}
              checked={theme === th.id}
              onChange={() => setTheme(th.id)}
              className="text-accent-600 focus:ring-accent-500"
            />
            {t(th.labelKey)}
          </label>
        ))}
      </div>
    </Panel>
  );
}
