import { useEffect, useMemo, useState, type ReactNode } from 'react';
import type { StringKey } from '@/i18n/strings';
import { SettingsProfile } from './SettingsProfile';
import { SettingsNotifications } from './SettingsNotifications';
import { SettingsSecurity } from './SettingsSecurity';
import { SettingsAccount } from './SettingsAccount';
import { SettingsTheme } from './SettingsTheme';

export type SettingsTab =
  | 'profile'
  | 'notifications'
  | 'security'
  | 'account'
  | 'theme'
  | 'subscription';

const TABS: { id: SettingsTab; key: StringKey }[] = [
  { id: 'profile', key: 'settings.sectionProfile' },
  { id: 'notifications', key: 'settings.sectionNotifications' },
  { id: 'security', key: 'settings.sectionSecurity' },
  { id: 'account', key: 'settings.sectionAccount' },
  { id: 'theme', key: 'settings.sectionTheme' },
  { id: 'subscription', key: 'settings.sectionSubscription' },
];

export function SettingsPage({
  t,
  subscriptionPanel,
  initialTab,
}: {
  t: (k: StringKey, fallback?: string) => string;
  /** Slim billing / complete-payment content for the Subscription tab. */
  subscriptionPanel?: ReactNode;
  initialTab?: SettingsTab;
}) {
  const [active, setActive] = useState<SettingsTab>(initialTab ?? 'profile');

  useEffect(() => {
    if (initialTab) setActive(initialTab);
  }, [initialTab]);

  const section = useMemo(() => {
    switch (active) {
      case 'profile':
        return <SettingsProfile t={t} />;
      case 'notifications':
        return <SettingsNotifications t={t} />;
      case 'security':
        return <SettingsSecurity t={t} />;
      case 'account':
        return <SettingsAccount t={t} />;
      case 'theme':
        return <SettingsTheme t={t} />;
      case 'subscription':
        return subscriptionPanel ?? null;
    }
  }, [active, t, subscriptionPanel]);

  return (
    <div className="space-y-6">
      <div className="border-b border-muted">
        <nav className="-mb-px flex flex-wrap gap-x-6 gap-y-2" aria-label="Settings sections">
          {TABS.map((tab) => (
            <button
              key={tab.id}
              type="button"
              onClick={() => setActive(tab.id)}
              className={`border-b-2 px-1 pb-3 text-sm font-medium transition-colors ${
                active === tab.id
                  ? 'border-accent-600 text-accent-700'
                  : 'border-transparent text-secondary hover:border-muted hover:text-main'
              }`}
            >
              {t(tab.key)}
            </button>
          ))}
        </nav>
      </div>
      {section}
    </div>
  );
}
