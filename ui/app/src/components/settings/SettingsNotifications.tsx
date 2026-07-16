import { useState, useCallback, useEffect } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import {
  fetchNotificationPrefs,
  updateNotificationPrefs,
  type NotificationPrefs,
} from '@/api/profile';
import { Panel } from '@/components/dashboard/Panel';
import { useToast } from '@/hooks/useToast';
import type { StringKey } from '@/i18n/strings';

export function SettingsNotifications({ t }: { t: (k: StringKey, fallback?: string) => string }) {
  const { push } = useToast();

  const [digest, setDigest] = useState<'daily' | 'weekly' | 'off'>('weekly');
  const [matchAlerts, setMatchAlerts] = useState(true);
  const [weeklySummary, setWeeklySummary] = useState(true);
  const [marketing, setMarketing] = useState(false);
  const [loaded, setLoaded] = useState(false);

  const prefsQuery = useQuery({
    queryKey: ['me', 'notifications'],
    queryFn: fetchNotificationPrefs,
    staleTime: 60_000,
  });

  useEffect(() => {
    if (!prefsQuery.data || loaded) return;
    const d = prefsQuery.data;
    if (d.email_digest === 'daily' || d.email_digest === 'weekly' || d.email_digest === 'off') {
      setDigest(d.email_digest);
    }
    setMatchAlerts(!!d.match_alerts);
    setWeeklySummary(!!d.weekly_summary);
    setMarketing(!!d.marketing_emails);
    setLoaded(true);
  }, [prefsQuery.data, loaded]);

  const mutation = useMutation({
    mutationFn: (prefs: NotificationPrefs) => updateNotificationPrefs(prefs),
    onSuccess: () => push(t('settings.notificationsSaved'), 'success'),
    onError: () => push(t('settings.notificationsFailed'), 'error'),
  });

  const handleSave = useCallback(() => {
    mutation.mutate({
      email_digest: digest,
      match_alerts: matchAlerts,
      weekly_summary: weeklySummary,
      marketing_emails: marketing,
    });
  }, [digest, matchAlerts, weeklySummary, marketing, mutation]);

  const DIGEST_OPTIONS: { value: 'daily' | 'weekly' | 'off'; labelKey: StringKey }[] = [
    { value: 'daily', labelKey: 'settings.daily' },
    { value: 'weekly', labelKey: 'settings.weekly' },
    { value: 'off', labelKey: 'settings.off' },
  ];

  return (
    <Panel title={t('settings.notifications')}>
      <div className="space-y-5">
        <fieldset>
          <legend className="text-sm font-medium text-gray-700 dark:text-gray-200">
            {t('settings.emailDigest')}
          </legend>
          <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {t('settings.emailDigestHint')}
          </p>
          <div className="mt-2 flex flex-wrap gap-4">
            {DIGEST_OPTIONS.map((opt) => (
              <label
                key={opt.value}
                className="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300"
              >
                <input
                  type="radio"
                  name="digest"
                  value={opt.value}
                  checked={digest === opt.value}
                  onChange={() => setDigest(opt.value)}
                  className="text-accent-600 focus:ring-accent-500"
                />
                {t(opt.labelKey)}
              </label>
            ))}
          </div>
        </fieldset>

        <div className="flex items-center justify-between gap-4">
          <div>
            <p className="text-sm font-medium text-gray-700 dark:text-gray-200">
              {t('settings.matchAlerts')}
            </p>
            <p className="text-xs text-gray-500 dark:text-gray-400">
              {t('settings.matchAlertsHint')}
            </p>
          </div>
          <label className="relative inline-flex cursor-pointer items-center">
            <input
              type="checkbox"
              checked={matchAlerts}
              onChange={(e) => setMatchAlerts(e.target.checked)}
              className="peer sr-only"
            />
            <div className="h-5 w-9 rounded-full bg-gray-200 after:absolute after:left-[2px] after:top-[2px] after:h-4 after:w-4 after:rounded-full after:bg-white after:transition-all peer-checked:bg-accent-600 peer-checked:after:translate-x-full dark:bg-navy-700" />
          </label>
        </div>

        <div className="flex items-center justify-between gap-4">
          <div>
            <p className="text-sm font-medium text-gray-700 dark:text-gray-200">
              {t('settings.weeklySummary')}
            </p>
            <p className="text-xs text-gray-500 dark:text-gray-400">
              {t('settings.weeklySummaryHint')}
            </p>
          </div>
          <label className="relative inline-flex cursor-pointer items-center">
            <input
              type="checkbox"
              checked={weeklySummary}
              onChange={(e) => setWeeklySummary(e.target.checked)}
              className="peer sr-only"
            />
            <div className="h-5 w-9 rounded-full bg-gray-200 after:absolute after:left-[2px] after:top-[2px] after:h-4 after:w-4 after:rounded-full after:bg-white after:transition-all peer-checked:bg-accent-600 peer-checked:after:translate-x-full dark:bg-navy-700" />
          </label>
        </div>

        <div className="flex items-center justify-between gap-4">
          <div>
            <p className="text-sm font-medium text-gray-700 dark:text-gray-200">
              {t('settings.marketingEmails')}
            </p>
            <p className="text-xs text-gray-500 dark:text-gray-400">
              {t('settings.marketingEmailsHint')}
            </p>
          </div>
          <label className="relative inline-flex cursor-pointer items-center">
            <input
              type="checkbox"
              checked={marketing}
              onChange={(e) => setMarketing(e.target.checked)}
              className="peer sr-only"
            />
            <div className="h-5 w-9 rounded-full bg-gray-200 after:absolute after:left-[2px] after:top-[2px] after:h-4 after:w-4 after:rounded-full after:bg-white after:transition-all peer-checked:bg-accent-600 peer-checked:after:translate-x-full dark:bg-navy-700" />
          </label>
        </div>

        <div className="pt-2">
          <button
            type="button"
            onClick={handleSave}
            disabled={mutation.isPending || prefsQuery.isLoading}
            className="min-h-[44px] rounded-md bg-navy-900 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-navy-800 disabled:opacity-50 dark:bg-accent-600 dark:hover:bg-accent-500"
          >
            {mutation.isPending ? t('common.loading') : t('settings.saveNotifications')}
          </button>
        </div>
      </div>
    </Panel>
  );
}
