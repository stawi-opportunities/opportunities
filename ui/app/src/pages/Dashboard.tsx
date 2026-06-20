import { useEffect, useRef } from 'react';
import { mount as mountProfile, type MountHandle } from '@stawi/profile';
import { authRuntime } from '@/auth/runtime';
import { profileWidgetTokens, profileWidgetCSS } from '@/theme/profile-widget';
import { useAuth } from '@/providers/AuthProvider';
import { getConfig } from '@/utils/config';
import { useSubscription } from '@/hooks/useSubscription';
import { normalizePlan } from '@/utils/plans';
import { DashboardHeader } from '@/components/dashboard/DashboardHeader';
import { AgentCard } from '@/components/dashboard/AgentCard';
import { BillingPanel } from '@/components/dashboard/BillingPanel';
import { PreferencesPanel } from '@/components/dashboard/PreferencesPanel';
import { CompletePaymentPanel } from '@/components/dashboard/CompletePaymentPanel';
import { PendingCheckoutPoller } from '@/components/dashboard/PendingCheckoutPoller';
import { WelcomeBanner } from '@/components/dashboard/WelcomeBanner';
import { OpportunitiesFeed } from '@/components/OpportunitiesFeed';
import { useI18n } from '@/i18n/I18nProvider';

export default function Dashboard() {
  const { state, login } = useAuth();
  const { t } = useI18n();

  const subQ = useSubscription();

  useEffect(() => {
    if (state !== 'authenticated') return;
    if (subQ.isLoading) return;
    if (subQ.data?.status !== 'active') {
      window.location.assign('/onboarding/');
    }
  }, [state, subQ.isLoading, subQ.data?.status]);

  if (state === 'initializing') return <Skeleton />;
  if (state !== 'authenticated') return <SignedOut onSignIn={login} />;

  const sub = subQ.data;
  const plan = normalizePlan(sub?.plan ?? null);
  const isActive = sub?.status === 'active';
  const subscription = sub?.status ?? 'none';

  return (
    <div className="mx-auto max-w-6xl px-4 py-8 sm:px-6 lg:px-8">
      <DashboardHeader plan={plan} status={subscription} t={t} />
      <PendingCheckoutPoller />
      {isActive && <div className="mt-4"><WelcomeBanner t={t} /></div>}

      <div className="mt-8 grid gap-8 lg:grid-cols-[320px_1fr]">
        <aside>
          <ProfileMount />
        </aside>
        <section className="space-y-6">
          {plan === null || !isActive ? (
            <CompletePaymentPanel plan={plan} status={subscription} />
          ) : (
            <>
              {plan === 'managed' && sub?.agent && <AgentCard agent={sub.agent} />}
              <OpportunitiesFeed />
            </>
          )}
          <PreferencesPanel />
          {plan && isActive && <BillingPanel plan={plan} renewsAt={sub?.renews_at} />}
        </section>
      </div>
    </div>
  );
}

function ProfileMount() {
  const hostRef = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    let handle: MountHandle | null = null;
    try {
      const cfg = getConfig();
      handle = mountProfile({
        target: host,
        runtime: authRuntime(),
        clientId: cfg.oidcClientID,
        installationId: cfg.oidcInstallationID,
        idpBaseUrl: cfg.oidcIssuer,
        apiBaseUrl: cfg.candidatesAPIURL,
        theme: 'light',
        tokens: profileWidgetTokens,
        css: profileWidgetCSS,
        onLogout: () => {
          window.location.href = '/';
        },
      });
    } catch {
      // Widget failed to mount — skeleton stays visible; sign out from nav.
    }
    return () => handle?.unmount();
  }, []);
  return <div ref={hostRef} className="min-h-[320px]" />;
}

function SignedOut({ onSignIn }: { onSignIn: () => Promise<void> }) {
  const { t } = useI18n();
  return (
    <div className="mx-auto max-w-md py-16 text-center">
      <h1 className="text-2xl font-semibold text-gray-900">{t('dash.signInTitle')}</h1>
      <p className="mt-2 text-gray-600">{t('dash.signInHint')}</p>
      <button
        type="button"
        onClick={() => void onSignIn()}
        className="mt-6 rounded-md bg-navy-900 px-5 py-2.5 text-sm font-medium text-white shadow-sm hover:bg-navy-800"
      >
        {t('nav.signIn')}
      </button>
    </div>
  );
}

function Skeleton() {
  return (
    <div className="mx-auto max-w-6xl px-4 py-8 sm:px-6 lg:px-8">
      <div className="flex animate-pulse items-end justify-between gap-4">
        <div className="space-y-2">
          <div className="h-8 w-48 rounded bg-gray-100" />
          <div className="flex items-center gap-2">
            <div className="h-5 w-16 rounded-full bg-gray-100" />
            <div className="h-4 w-64 rounded bg-gray-100" />
          </div>
        </div>
        <div className="flex gap-3">
          <div className="h-5 w-20 rounded bg-gray-100" />
          <div className="h-10 w-28 rounded-md bg-gray-100" />
        </div>
      </div>
      <div className="mt-8 grid animate-pulse gap-8 lg:grid-cols-[320px_1fr]">
        <aside className="space-y-4">
          <div className="flex flex-col items-center gap-3 rounded-lg border border-gray-200 bg-white p-6">
            <div className="h-16 w-16 rounded-full bg-gray-100" />
            <div className="h-4 w-24 rounded bg-gray-100" />
            <div className="h-3 w-32 rounded bg-gray-100" />
            <div className="h-3 w-20 rounded bg-gray-100" />
          </div>
        </aside>
        <section className="space-y-3">
          {[1, 2, 3].map((i) => (
            <div key={i} className="rounded-lg border border-gray-200 bg-white p-4">
              <div className="h-4 w-3/4 rounded bg-gray-100" />
              <div className="mt-2 h-3 w-1/2 rounded bg-gray-100" />
              <div className="mt-3 flex gap-2">
                <div className="h-8 w-20 rounded bg-gray-100" />
                <div className="h-8 w-20 rounded bg-gray-100" />
              </div>
            </div>
          ))}
        </section>
      </div>
    </div>
  );
}
