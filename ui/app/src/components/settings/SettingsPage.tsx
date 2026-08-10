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
    <div className="ds-stack">
      <div>
        <h2 className="ds-section-title">Settings</h2>
        <p className="ds-section-desc">Notifications, account, and subscription — one place.</p>
      </div>
      <div className="border-b border-muted">
        <nav
          className="-mb-px flex gap-1 overflow-x-auto overscroll-x-contain pb-px [-ms-overflow-style:none] [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
          aria-label="Settings sections"
        >
          {TABS.map((tab) => (
            <button
              key={tab.id}
              type="button"
              onClick={() => setActive(tab.id)}
              className={`min-h-[44px] shrink-0 border-b-2 px-3 text-sm font-medium transition-colors sm:px-4 ${
                active === tab.id
                  ? 'border-accent-600 text-main'
                  : 'border-transparent text-secondary hover:text-main'
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
