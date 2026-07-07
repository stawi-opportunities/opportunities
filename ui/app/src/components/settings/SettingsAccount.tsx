import { useState, useCallback } from 'react';
import { useMutation } from '@tanstack/react-query';
import { deleteAccount, requestDataExport } from '@/api/profile';
import { Panel } from '@/components/dashboard/Panel';
import { useToast } from '@/hooks/useToast';
import type { StringKey } from '@/i18n/strings';

export function SettingsAccount({ t }: { t: (k: StringKey, fallback?: string) => string }) {
  const { push } = useToast();
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);
  const [deleteReason, setDeleteReason] = useState('');

  const dataExportMutation = useMutation({
    mutationFn: () => requestDataExport(),
    onSuccess: () => push(t('settings.dataExportRequested'), 'success'),
    onError: () => push('Failed to request data export.', 'error'),
  });

  const deleteMutation = useMutation({
    mutationFn: () => deleteAccount(deleteReason || undefined),
    onSuccess: () => {
      push(t('settings.accountDeleted'), 'success');
      setTimeout(() => {
        window.location.href = '/';
      }, 2000);
    },
    onError: () => push('Failed to delete account.', 'error'),
  });

  const handleDataExport = useCallback(() => {
    dataExportMutation.mutate();
  }, [dataExportMutation]);

  const handleDeleteAccount = useCallback(() => {
    deleteMutation.mutate();
  }, [deleteMutation]);

  return (
    <div className="space-y-6">
      <Panel title={t('settings.dataExport')}>
        <p className="text-sm text-gray-500">{t('settings.dataExportHint')}</p>
        <button
          type="button"
          onClick={handleDataExport}
          disabled={dataExportMutation.isPending}
          className="mt-3 rounded-md border border-gray-300 px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50"
        >
          {dataExportMutation.isPending ? t('common.loading') : t('settings.dataExport')}
        </button>
      </Panel>

      <Panel title={t('settings.deleteAccount')}>
        {!showDeleteConfirm ? (
          <>
            <p className="text-sm text-gray-500">{t('settings.deleteAccountWarning')}</p>
            <button
              type="button"
              onClick={() => setShowDeleteConfirm(true)}
              className="mt-3 rounded-md border border-red-300 px-3 py-1.5 text-sm font-medium text-red-700 hover:bg-red-50"
            >
              {t('settings.deleteAccount')}
            </button>
          </>
        ) : (
          <div className="space-y-4">
            <p className="text-sm font-medium text-red-700">{t('settings.deleteConfirm')}</p>
            <div>
              <label htmlFor="delete-reason" className="block text-sm text-gray-600">
                {t('settings.deleteReason')}
              </label>
              <textarea
                id="delete-reason"
                rows={2}
                value={deleteReason}
                onChange={(e) => setDeleteReason(e.target.value)}
                className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-accent-500 focus:outline-none focus:ring-1 focus:ring-accent-500"
              />
            </div>
            <div className="flex gap-3">
              <button
                type="button"
                onClick={handleDeleteAccount}
                disabled={deleteMutation.isPending}
                className="rounded-md bg-red-700 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-red-800 disabled:opacity-50"
              >
                {deleteMutation.isPending ? t('common.loading') : t('settings.deleteAccount')}
              </button>
              <button
                type="button"
                onClick={() => {
                  setShowDeleteConfirm(false);
                  setDeleteReason('');
                }}
                disabled={deleteMutation.isPending}
                className="rounded-md border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
              >
                Cancel
              </button>
            </div>
          </div>
        )}
      </Panel>
    </div>
  );
}
