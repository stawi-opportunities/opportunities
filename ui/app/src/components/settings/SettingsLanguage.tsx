import { Panel } from '@/components/dashboard/Panel';
import type { StringKey } from '@/i18n/strings';

export function SettingsLanguage({ t }: { t: (k: StringKey, fallback?: string) => string }) {
  const LANGUAGES = [
    { code: 'en', label: 'English' },
    { code: 'es', label: 'Español' },
    { code: 'fr', label: 'Français' },
    { code: 'de', label: 'Deutsch' },
    { code: 'pt', label: 'Português' },
    { code: 'ja', label: '日本語' },
    { code: 'ar', label: 'العربية' },
    { code: 'zh', label: '中文' },
  ];

  return (
    <div className="space-y-6">
      <Panel title={t('settings.uiLanguage')}>
        <select className="mt-1 block w-full max-w-xs rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-accent-500 focus:outline-none focus:ring-1 focus:ring-accent-500">
          {LANGUAGES.map((lang) => (
            <option key={lang.code} value={lang.code}>
              {lang.label}
            </option>
          ))}
        </select>
        <p className="mt-2 text-xs text-gray-500">{t('settings.comingSoon')}</p>
      </Panel>

      <Panel title={t('settings.workingLanguages')}>
        <p className="text-sm text-gray-500">{t('settings.comingSoon')}</p>
      </Panel>

      <Panel title={t('settings.country')}>
        <p className="text-sm text-gray-500">{t('settings.comingSoon')}</p>
      </Panel>
    </div>
  );
}
