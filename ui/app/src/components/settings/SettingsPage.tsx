import { useEffect, useMemo, useState, type ReactNode } from 'react';
import type { StringKey } from '@/i18n/strings';
import { SettingsNotifications } from './SettingsNotifications';
import { SettingsAccount } from './SettingsAccount';

export type SettingsTab = 'notifications' | 'account' | 'subscription';

const TABS: { id: SettingsTab; key: StringKey }[] = [
  { id: 'notifications', key: 'settings.sectionNotifications' },
  { id: 'account', key: 'settings.sectionAccount' },
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
  const [active, setActive] = useState<SettingsTab>(initialTab ?? 'notifications');

  useEffect(() => {
    if (initialTab) setActive(initialTab);
  }, [initialTab]);

  const section = useMemo(() => {
    switch (active) {
      case 'notifications':
        return <SettingsNotifications t={t} />;
      case 'account':
        return <SettingsAccount t={t} />;
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
