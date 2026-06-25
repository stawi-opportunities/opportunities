import { useEffect, useRef, useState, useCallback } from 'react';
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
import { MatchesPanel } from '@/components/dashboard/MatchesPanel';
import { OpportunitiesFeed } from '@/components/OpportunitiesFeed';
import { DashboardSidebar, type SectionId } from '@/components/dashboard/DashboardSidebar';
import { DashboardBreadcrumbs } from '@/components/dashboard/DashboardBreadcrumbs';
import { PlanChangeModal } from '@/components/dashboard/PlanChangeModal';
import { CancelSubscriptionModal } from '@/components/dashboard/CancelSubscriptionModal';
import { SettingsPage } from '@/components/settings/SettingsPage';
import { ErrorBoundary } from '@/components/common/ErrorBoundary';
import { useI18n } from '@/i18n/I18nProvider';
import { useDocumentTitle } from '@/hooks/useDocumentTitle';
import { useTheme } from '@/providers/ThemeProvider';

function getSectionFromHash(): SectionId {
  const hash = window.location.hash.replace('#', '');
  const valid: SectionId[] = ['feed', 'matches', 'saved', 'preferences', 'billing', 'settings'];
  return valid.includes(hash as SectionId) ? (hash as SectionId) : 'feed';
}

export default function Dashboard() {
  const { state, login } = useAuth();
  const { t } = useI18n();
  const [activeSection, setActiveSection] = useState<SectionId>(getSectionFromHash);
  const [showPlanChange, setShowPlanChange] = useState(false);
  const [showCancel, setShowCancel] = useState(false);

  const subQ = useSubscription();

  const sectionLabels: Record<SectionId, string> = {
    feed: 'Feed',
    matches: 'Matches',
    saved: 'Saved',
    preferences: 'Preferences',
    billing: 'Billing',
    settings: 'Settings',
  };
  useDocumentTitle(`Dashboard — ${sectionLabels[activeSection]} | Stawi`);

  useEffect(() => {
    const onHashChange = () => setActiveSection(getSectionFromHash());
    window.addEventListener('hashchange', onHashChange);
    return () => window.removeEventListener('hashchange', onHashChange);
  }, []);

  useEffect(() => {
    if (state !== 'authenticated') return;
    if (subQ.isLoading) return;
    if (subQ.data?.status !== 'active') {
      window.location.assign('/onboarding/');
    }
  }, [state, subQ.isLoading, subQ.data?.status]);

  const navigate = (id: SectionId) => {
    window.location.hash = id;
    setActiveSection(id);
  };

  const handlePlanChangeSuccess = useCallback(() => {
    setShowPlanChange(false);
    subQ.refetch();
  }, [subQ]);

  const handleCancelSuccess = useCallback(() => {
    setShowCancel(false);
    subQ.refetch();
  }, [subQ]);

  if (state === 'initializing') return <Skeleton />;
  if (state !== 'authenticated') return <SignedOut onSignIn={login} />;

  const sub = subQ.data;
  const plan = normalizePlan(sub?.plan ?? null);
  const isActive = sub?.status === 'active';
  const subscription = sub?.status ?? 'none';

  return (
    <div className="mx-auto max-w-6xl px-4 py-8 sm:px-6 lg:px-8">
      <DashboardHeader
        plan={plan}
        status={subscription}
        onOpenPlanChange={() => setShowPlanChange(true)}
        t={t}
      />
      <PendingCheckoutPoller />
      {isActive && (
        <div className="mt-4">
          <WelcomeBanner t={t} />
        </div>
      )}

      <div className="mt-4">
        <DashboardBreadcrumbs active={activeSection} t={t} />
      </div>

      <div className="mt-4 lg:hidden">
        <DashboardSidebar
          active={activeSection}
          onNavigate={navigate}
          t={t}
          matchCount={sub?.queued_matches}
        />
      </div>

      <div className="mt-8 grid gap-8 lg:grid-cols-[240px_1fr]">
        <aside className="hidden lg:block">
          <DashboardSidebar
            active={activeSection}
            onNavigate={navigate}
            t={t}
            matchCount={sub?.queued_matches}
          />
          <div className="mt-6">
            <ProfileMount />
          </div>
        </aside>
        <section className="space-y-6">
          {plan === null || !isActive ? (
            <CompletePaymentPanel plan={plan} status={subscription} />
          ) : (
            <>
              {activeSection === 'feed' && (
                <ErrorBoundary>
                  {plan === 'managed' && sub?.agent && <AgentCard agent={sub.agent} />}
                  <OpportunitiesFeed />
                </ErrorBoundary>
              )}
              {activeSection === 'matches' && (
                <ErrorBoundary>
                  <MatchesPanel
                    plan={plan}
                    queued={sub?.queued_matches ?? null}
                    delivered={sub?.delivered_this_week ?? null}
                    subQueryError={subQ.isError}
                    onUpgrade={() => setShowPlanChange(true)}
                  />
                </ErrorBoundary>
              )}
              {activeSection === 'saved' && (
                <ErrorBoundary>
                  <div className="rounded-lg border border-gray-200 bg-white p-6 text-center text-gray-500 dark:border-navy-700 dark:bg-navy-900 dark:text-gray-400">
                    Saved opportunities will appear here.
                  </div>
                </ErrorBoundary>
              )}
              {activeSection === 'preferences' && (
                <ErrorBoundary>
                  <PreferencesPanel />
                </ErrorBoundary>
              )}
              {activeSection === 'billing' && plan && isActive && (
                <ErrorBoundary>
                  <BillingPanel
                    plan={plan}
                    renewsAt={sub?.renews_at}
                    onOpenPlanChange={() => setShowPlanChange(true)}
                    onOpenCancel={() => setShowCancel(true)}
                    t={t}
                  />
                </ErrorBoundary>
              )}
              {activeSection === 'settings' && (
                <ErrorBoundary>
                  <SettingsPage t={t} />
                </ErrorBoundary>
              )}
            </>
          )}
        </section>
      </div>

      {/* Mobile profile — below lg */}
      <div className="mt-8 lg:hidden">
        <ProfileMount />
      </div>

      {showPlanChange && plan && (
        <PlanChangeModal
          currentPlan={plan}
          onClose={() => setShowPlanChange(false)}
          t={t}
          onSuccess={handlePlanChangeSuccess}
        />
      )}

      {showCancel && (
        <CancelSubscriptionModal
          onClose={() => setShowCancel(false)}
          t={t}
          onSuccess={handleCancelSuccess}
        />
      )}
    </div>
  );
}

function ProfileMount() {
  const { resolved: resolvedTheme } = useTheme();
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
        theme: resolvedTheme,
        tokens: profileWidgetTokens,
        css: profileWidgetCSS,
        onLogout: () => {
          window.location.href = '/';
        },
      });
    } catch (_e) {
      // Widget mount is best-effort; skeleton stays visible on failure.
    }
    return () => handle?.unmount();
  }, []);
  return <div ref={hostRef} className="min-h-[320px]" />;
}

function SignedOut({ onSignIn }: { onSignIn: () => Promise<void> }) {
  const { t } = useI18n();
  return (
    <div className="mx-auto max-w-md py-16 text-center">
      <h1 className="text-2xl font-semibold text-gray-900 dark:text-white">
        {t('dash.signInTitle')}
      </h1>
      <p className="mt-2 text-gray-600 dark:text-gray-300">{t('dash.signInHint')}</p>
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
          <div className="h-8 w-48 rounded bg-gray-100 dark:bg-navy-800" />
          <div className="flex items-center gap-2">
            <div className="h-5 w-16 rounded-full bg-gray-100 dark:bg-navy-800" />
            <div className="h-4 w-64 rounded bg-gray-100 dark:bg-navy-800" />
          </div>
        </div>
        <div className="flex gap-3">
          <div className="h-5 w-20 rounded bg-gray-100 dark:bg-navy-800" />
          <div className="h-10 w-28 rounded-md bg-gray-100 dark:bg-navy-800" />
        </div>
      </div>
      <div className="mt-8 grid animate-pulse gap-8 lg:grid-cols-[240px_1fr]">
        <aside className="space-y-4">
          <div className="flex flex-col items-center gap-3 rounded-lg border border-gray-200 bg-white p-6 dark:border-navy-700 dark:bg-navy-900">
            <div className="h-16 w-16 rounded-full bg-gray-100 dark:bg-navy-800" />
            <div className="h-4 w-24 rounded bg-gray-100 dark:bg-navy-800" />
            <div className="h-3 w-32 rounded bg-gray-100 dark:bg-navy-800" />
            <div className="h-3 w-20 rounded bg-gray-100 dark:bg-navy-800" />
          </div>
        </aside>
        <section className="space-y-3">
          {[1, 2, 3].map((i) => (
            <div
              key={i}
              className="rounded-lg border border-gray-200 bg-white p-4 dark:border-navy-700 dark:bg-navy-900"
            >
              <div className="h-4 w-3/4 rounded bg-gray-100 dark:bg-navy-800" />
              <div className="mt-2 h-3 w-1/2 rounded bg-gray-100 dark:bg-navy-800" />
              <div className="mt-3 flex gap-2">
                <div className="h-8 w-20 rounded bg-gray-100 dark:bg-navy-800" />
                <div className="h-8 w-20 rounded bg-gray-100 dark:bg-navy-800" />
              </div>
            </div>
          ))}
        </section>
      </div>
    </div>
  );
}
