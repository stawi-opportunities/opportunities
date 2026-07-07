import { useState, useCallback } from 'react';
import { useMutation } from '@tanstack/react-query';
import { changePassword } from '@/api/profile';
import { Panel } from '@/components/dashboard/Panel';
import { useToast } from '@/hooks/useToast';
import type { StringKey } from '@/i18n/strings';

export function SettingsSecurity({ t }: { t: (k: StringKey, fallback?: string) => string }) {
  const { push } = useToast();

  const [currentPassword, setCurrentPassword] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [passwordError, setPasswordError] = useState<string | null>(null);

  const changePwMutation = useMutation({
    mutationFn: () => changePassword(currentPassword, newPassword),
    onSuccess: () => {
      push(t('settings.passwordChanged'), 'success');
      setCurrentPassword('');
      setNewPassword('');
      setConfirmPassword('');
      setPasswordError(null);
    },
    onError: () => {
      push(t('settings.passwordChanged'), 'error');
    },
  });

  const handleChangePassword = useCallback(() => {
    setPasswordError(null);
    if (newPassword !== confirmPassword) {
      setPasswordError(t('settings.passwordMismatch'));
      return;
    }
    if (newPassword.length < 8) {
      setPasswordError(t('settings.passwordWeak'));
      return;
    }
    if (newPassword === currentPassword) {
      setPasswordError(t('settings.passwordSame'));
      return;
    }
    changePwMutation.mutate();
  }, [currentPassword, newPassword, confirmPassword, t, changePwMutation]);

  return (
    <div className="space-y-6">
      <Panel title={t('settings.changePassword')}>
        <div className="space-y-4">
          <div>
            <label
              htmlFor="settings-current-pw"
              className="block text-sm font-medium text-gray-700"
            >
              {t('settings.currentPassword')}
            </label>
            <input
              id="settings-current-pw"
              type="password"
              value={currentPassword}
              onChange={(e) => setCurrentPassword(e.target.value)}
              className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-accent-500 focus:outline-none focus:ring-1 focus:ring-accent-500"
            />
          </div>
          <div>
            <label htmlFor="settings-new-pw" className="block text-sm font-medium text-gray-700">
              {t('settings.newPassword')}
            </label>
            <input
              id="settings-new-pw"
              type="password"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-accent-500 focus:outline-none focus:ring-1 focus:ring-accent-500"
            />
          </div>
          <div>
            <label
              htmlFor="settings-confirm-pw"
              className="block text-sm font-medium text-gray-700"
            >
              {t('settings.confirmPassword')}
            </label>
            <input
              id="settings-confirm-pw"
              type="password"
              value={confirmPassword}
              onChange={(e) => setConfirmPassword(e.target.value)}
              className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm shadow-sm focus:border-accent-500 focus:outline-none focus:ring-1 focus:ring-accent-500"
            />
          </div>
          {passwordError && <p className="text-sm text-red-600">{passwordError}</p>}
          <div className="pt-1">
            <button
              type="button"
              onClick={handleChangePassword}
              disabled={changePwMutation.isPending}
              className="rounded-md bg-navy-900 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-navy-800 disabled:opacity-50"
            >
              {changePwMutation.isPending ? t('common.loading') : t('settings.changePassword')}
            </button>
          </div>
        </div>
      </Panel>

      <Panel title={t('settings.twoFactor')}>
        <p className="text-sm text-gray-500">{t('settings.twoFactorDisabled')}</p>
        <button
          type="button"
          className="mt-3 rounded-md border border-gray-300 px-3 py-1.5 text-sm font-medium text-gray-700 opacity-50"
          disabled
        >
          {t('settings.enable2FA')}
        </button>
        <p className="mt-2 text-xs text-gray-500">{t('settings.comingSoon')}</p>
      </Panel>

      <Panel title={t('settings.activeSessions')}>
        <p className="text-sm text-gray-500">{t('settings.sessionManagementUnavailable')}</p>
      </Panel>
    </div>
  );
}
