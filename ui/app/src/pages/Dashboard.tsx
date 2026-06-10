import { useEffect, useRef } from "react";
import { mount as mountProfile, type MountHandle } from "@stawi/profile";
import { authRuntime } from "@/auth/runtime";
import { profileWidgetTokens, profileWidgetCSS } from "@/theme/profile-widget";
import { useAuth } from "@/providers/AuthProvider";
import { getConfig } from "@/utils/config";
import { useSubscription } from "@/hooks/useSubscription";
import { normalizePlan } from "@/utils/plans";
import { DashboardHeader } from "@/components/dashboard/DashboardHeader";
import { AgentCard } from "@/components/dashboard/AgentCard";
import { BillingPanel } from "@/components/dashboard/BillingPanel";
import { PreferencesPanel } from "@/components/dashboard/PreferencesPanel";
import { CompletePaymentPanel } from "@/components/dashboard/CompletePaymentPanel";
import { PendingCheckoutPoller } from "@/components/dashboard/PendingCheckoutPoller";
import { OpportunitiesFeed } from "@/components/OpportunitiesFeed";
import { useI18n } from "@/i18n/I18nProvider";

export default function Dashboard() {
  const { state, login } = useAuth();
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

  return (
    <div className="mx-auto max-w-6xl px-4 py-8 sm:px-6 lg:px-8">
      <DashboardHeader plan={plan} active={isActive} />
      <PendingCheckoutPoller />

      <div className="mt-8 grid gap-8 lg:grid-cols-[320px_1fr]">
        <aside>
          <ProfileMount />
        </aside>
        <section className="space-y-6">
          {plan === null || !isActive ? (
            <CompletePaymentPanel plan={plan} status={sub?.status ?? 'none'} />
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
      <div className="h-8 w-48 animate-pulse rounded bg-gray-100" />
      <div className="mt-8 grid gap-8 lg:grid-cols-[320px_1fr]">
        <div className="h-96 animate-pulse rounded-lg bg-gray-100" />
        <div className="h-96 animate-pulse rounded-lg bg-gray-100" />
      </div>
    </div>
  );
}