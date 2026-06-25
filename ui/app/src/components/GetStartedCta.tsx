import { useI18n } from '@/i18n/I18nProvider';
import { useAuth } from '@/providers/AuthProvider';

export default function GetStartedCta() {
  const { t } = useI18n();
  const { state, login } = useAuth();

  if (state === 'authenticated') return null;

  const onClick = (e: React.MouseEvent<HTMLAnchorElement>) => {
    // login() is a full-page redirect to the IdP; after sign-in,
    // /auth/callback/ routes to /dashboard/ or /onboarding/ based on
    // subscription status. Going straight to login() (rather than the
    // href fallback) skips an extra hop through /onboarding/. The href
    // stays as the no-JS fallback.
    e.preventDefault();
    void login();
  };

  return (
    <section className="bg-white/90 py-20 shadow-sm ring-1 ring-gray-200">
      <div className="mx-auto flex max-w-5xl flex-col items-start justify-between gap-8 px-6 sm:flex-row sm:items-center sm:px-8 lg:px-12">
        <div>
          <h2 className="text-3xl font-bold text-navy-900 sm:text-4xl">{t('cta.twoMinutes')}</h2>
          <p className="mt-2 text-lg leading-relaxed text-gray-700">{t('cta.twoMinutesHint')}</p>
        </div>
        <a href="/onboarding/" onClick={onClick} className="btn-primary px-7 py-3 text-base">
          {t('cta.getStarted')}
        </a>
      </div>
    </section>
  );
}
