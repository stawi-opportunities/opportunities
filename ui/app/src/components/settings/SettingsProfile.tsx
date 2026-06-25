import { useState, useEffect, useCallback } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { fetchCandidate, updateProfile, uploadCV } from '@/api/profile';
import { Panel } from '@/components/dashboard/Panel';
import { useToast } from '@/hooks/useToast';
import type { StringKey } from '@/i18n/strings';

export function SettingsProfile({ t }: { t: (k: StringKey, fallback?: string) => string }) {
  const { push } = useToast();
  const { data: candidate, isLoading } = useQuery({
    queryKey: ['candidate'],
    queryFn: fetchCandidate,
  });

  const [name, setName] = useState('');
  const [currentTitle, setCurrentTitle] = useState('');
  const [phone, setPhone] = useState('');

  useEffect(() => {
    if (candidate) {
      setCurrentTitle(candidate.current_title || '');
    }
  }, [candidate]);

  const saveMutation = useMutation({
    mutationFn: (payload: { name: string; current_title: string; phone: string }) =>
      updateProfile({
        name: payload.name,
        current_title: payload.current_title,
        phone: payload.phone,
      }),
    onSuccess: () => push(t('settings.profileSaved'), 'success'),
    onError: () => push(t('settings.profileFailed'), 'error'),
  });

  const handleSave = useCallback(() => {
    saveMutation.mutate({ name, current_title: currentTitle, phone });
  }, [name, currentTitle, phone, saveMutation]);

  if (isLoading) {
    return (
      <Panel title={t('settings.profile')}>
        <div className="animate-pulse space-y-4">
          <div className="h-10 w-full rounded bg-gray-100" />
          <div className="h-10 w-full rounded bg-gray-100" />
          <div className="h-10 w-full rounded bg-gray-100" />
        </div>
      </Panel>
    );
  }

  return (
    <Panel title={t('settings.profile')}>
      <div className="space-y-4">
        <div>
          <label htmlFor="settings-name" className="block text-sm font-medium text-gray-700">
            {t('settings.name')}
          </label>
          <input
            id="settings-name"
            type="text"
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-accent-500 focus:outline-none focus:ring-1 focus:ring-accent-500"
          />
        </div>
        <div>
          <label htmlFor="settings-email" className="block text-sm font-medium text-gray-700">
            {t('settings.email')}
          </label>
          <input
            id="settings-email"
            type="email"
            value=""
            disabled
            placeholder="managed@idp.com"
            className="mt-1 block w-full cursor-not-allowed rounded-md border border-gray-200 bg-gray-50 px-3 py-2 text-sm text-gray-500"
          />
          <p className="mt-1 text-xs text-gray-500">{t('settings.managedByIdp')}</p>
        </div>
        <div>
          <label
            htmlFor="settings-currentTitle"
            className="block text-sm font-medium text-gray-700"
          >
            {t('settings.currentTitle')}
          </label>
          <input
            id="settings-currentTitle"
            type="text"
            value={currentTitle}
            onChange={(e) => setCurrentTitle(e.target.value)}
            className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-accent-500 focus:outline-none focus:ring-1 focus:ring-accent-500"
          />
        </div>
        <div>
          <label htmlFor="settings-phone" className="block text-sm font-medium text-gray-700">
            {t('settings.phone')}
          </label>
          <input
            id="settings-phone"
            type="tel"
            value={phone}
            onChange={(e) => setPhone(e.target.value)}
            className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-accent-500 focus:outline-none focus:ring-1 focus:ring-accent-500"
          />
        </div>
        <div>
          <p className="text-sm font-medium text-gray-700">{t('settings.cv')}</p>
          <p className="mt-1 text-sm text-gray-500">{t('settings.cvUploaded')}</p>
          <label className="mt-2 inline-flex cursor-pointer items-center gap-2 rounded-md border border-gray-300 px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50">
            {t('settings.cvUploadNew')}
            <input
              type="file"
              accept=".pdf,.doc,.docx"
              className="sr-only"
              onChange={async (e) => {
                const file = e.target.files?.[0];
                if (!file) return;
                try {
                  await uploadCV(file);
                  push(t('settings.profileSaved'), 'success');
                } catch {
                  push(t('settings.profileFailed'), 'error');
                }
              }}
            />
          </label>
        </div>
        <div className="pt-2">
          <button
            type="button"
            onClick={handleSave}
            disabled={saveMutation.isPending}
            className="rounded-md bg-navy-900 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-navy-800 disabled:opacity-50"
          >
            {saveMutation.isPending ? t('common.loading') : t('settings.saveProfile')}
          </button>
        </div>
      </div>
    </Panel>
  );
}
