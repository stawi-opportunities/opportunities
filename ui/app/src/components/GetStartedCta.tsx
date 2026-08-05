import { useI18n } from '@/i18n/I18nProvider';
import { useAuth } from '@/providers/AuthProvider';

export default function GetStartedCta() {
  const { t } = useI18n();
  const { hasSession, login } = useAuth();

  // Sticky session: hide while signed in *and* during token refresh.
  if (hasSession) return null;

  const onClick = (e: React.MouseEvent<HTMLButtonElement>) => {
    e.preventDefault();
    // Same OIDC path as nav Sign in — no hop through /onboarding/.
    void login();
  };

  return (
    <section className="bg-surface py-20 shadow-sm ring-1 ring-muted">
      <div className="mx-auto flex max-w-5xl flex-col items-start justify-between gap-8 px-6 sm:flex-row sm:items-center sm:px-8 lg:px-12">
        <div>
          <h2 className="text-3xl font-bold text-main sm:text-4xl">{t('cta.twoMinutes')}</h2>
          <p className="mt-2 text-lg leading-relaxed text-secondary">{t('cta.twoMinutesHint')}</p>
        </div>
        <button type="button" onClick={onClick} className="btn-primary px-7 py-3 text-base">
          {t('cta.getStarted')}
        </button>
      </div>
    </section>
  );
}
