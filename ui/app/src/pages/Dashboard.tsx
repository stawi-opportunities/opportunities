import { useEffect, useRef, useState, useCallback } from 'react';
import { mount as mountProfile, type MountHandle } from '@stawi/profile';
import { authRuntime } from '@/auth/runtime';
import { profileWidgetTokens, profileWidgetCSS } from '@/theme/profile-widget';
import { useAuth } from '@/providers/AuthProvider';
import { getConfig } from '@/utils/config';
import { useSubscription } from '@/hooks/useSubscription';
import { normalizePlan } from '@/utils/plans';
import { Button } from '@/components/ui/Button';
import { DashboardHeader } from '@/components/dashboard/DashboardHeader';
import { AgentCard } from '@/components/dashboard/AgentCard';
import { BillingPanel } from '@/components/dashboard/BillingPanel';
import { PreferencesPanel } from '@/components/dashboard/PreferencesPanel';
import { SavedJobsPanel } from '@/components/dashboard/SavedJobsPanel';
import { ApplicationsPanel } from '@/components/dashboard/ApplicationsPanel';
import { OverviewPanel } from '@/components/dashboard/OverviewPanel';
import { GuidedTour, isTourCompleted } from '@/components/dashboard/GuidedTour';
import { CompletePaymentPanel } from '@/components/dashboard/CompletePaymentPanel';
import { PendingCheckoutPoller } from '@/components/dashboard/PendingCheckoutPoller';
import { MatchesPanel } from '@/components/dashboard/MatchesPanel';
import { ToolsPanel } from '@/components/dashboard/ToolsPanel';
import { OpportunitiesFeed } from '@/components/OpportunitiesFeed';
import { DashboardSidebar, type SectionId } from '@/components/dashboard/DashboardSidebar';
import { DashboardBreadcrumbs } from '@/components/dashboard/DashboardBreadcrumbs';
import { PlanChangeModal } from '@/components/dashboard/PlanChangeModal';
import { CancelSubscriptionModal } from '@/components/dashboard/CancelSubscriptionModal';
import { SettingsPage } from '@/components/settings/SettingsPage';
import { ErrorBoundary } from '@/components/common/ErrorBoundary';
import { ProfileCompleteness } from '@/components/dashboard/ProfileCompleteness';
import { QuickActions } from '@/components/dashboard/QuickActions';
import { DashboardBanner } from '@/components/dashboard/DashboardBanner';
import { StatsRow } from '@/components/dashboard/StatsRow';
import { PreferenceChatHost } from '@/components/preference-chat';
import { useI18n } from '@/i18n/I18nProvider';
import { useDocumentTitle } from '@/hooks/useDocumentTitle';
import { useTheme } from '@/providers/ThemeProvider';

function getSectionFromHash(): SectionId {
  const hash = window.location.hash.replace('#', '');
  const valid: SectionId[] = [
    'overview',
    'feed',
    'matches',
    'tools',
    'saved',
    'applications',
    'preferences',
    'billing',
    'settings',
  ];
  return valid.includes(hash as SectionId) ? (hash as SectionId) : 'overview';
}

export default function Dashboard() {
  const { hasSession, ready, login } = useAuth();
  const { t } = useI18n();
  const [activeSection, setActiveSection] = useState<SectionId>(getSectionFromHash);
  const [showPlanChange, setShowPlanChange] = useState(false);
  const [showCancel, setShowCancel] = useState(false);
  const [showTour, setShowTour] = useState(false);

  const subQ = useSubscription();

  const sectionLabels: Record<SectionId, string> = {
    overview: 'Overview',
    feed: 'Feed',
    matches: 'Matches',
    tools: 'Tools',
    saved: 'Saved',
    applications: 'Applications',
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

  // Free users keep the dashboard: first-match proof + tools before pay.
  // Only bounce if profile was never started (no onboarding draft / no CV path).
  // Payment is optional upgrade, not a hard gate.

  useEffect(() => {
    if (!isTourCompleted()) {
      setShowTour(true);
    }
  }, []);

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

  // Pending session restore / sticky session: keep dashboard chrome stable.
  if (!ready && hasSession) return <Skeleton />;
  if (!ready && !hasSession) return <Skeleton />;
  // Definitive signed-out only — never during token refresh.
  if (!hasSession) return <SignedOut onSignIn={login} />;
  const sub = subQ.data;
  const plan = normalizePlan(sub?.plan ?? null);
  // past_due / trial keep full product access (grace / trial entitlement).
  const isActive =
    sub?.status === 'active' || sub?.status === 'past_due' || sub?.status === 'trial';
  const subscription = sub?.status ?? 'none';

  return (
    <PreferenceChatHost>
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
            <DashboardBanner onStartTour={() => setShowTour(true)} />
          </div>
        )}
        {isActive && (
          <div className="mt-4">
            <QuickActions />
          </div>
        )}
        <div className="sticky top-0 z-10 -mx-4 bg-white px-4 pb-2 dark:bg-navy-900 sm:static sm:mx-0 sm:px-0 sm:pb-0">
          <DashboardBreadcrumbs active={activeSection} t={t} />
        </div>

        <div className="mt-4 md:hidden">
          <DashboardSidebar
            active={activeSection}
            onNavigate={navigate}
            t={t}
            matchCount={sub?.queued_matches}
          />
        </div>

        <div className="mt-8 grid gap-8 lg:grid-cols-[240px_1fr]">
          <aside className="hidden md:block">
            <DashboardSidebar
              active={activeSection}
              onNavigate={navigate}
              t={t}
              matchCount={sub?.queued_matches}
            />
            <div className="mt-6">
              <ProfileCompleteness />
            </div>
            <div className="mt-6">
              <ProfileMount />
            </div>
          </aside>
          <section className="space-y-4 sm:space-y-6">
            {!isActive && (
              <div className="rounded-xl border border-accent-200 bg-accent-50/70 p-4 dark:border-accent-800 dark:bg-accent-950/30">
                <p className="text-sm font-medium text-gray-900 dark:text-white">
                  Free proof mode — get real matches before you pay
                </p>
                <p className="mt-1 text-sm text-gray-700 dark:text-gray-300">
                  Upload a CV, open Matches, and run <strong>Find matches now</strong>. Subscribe
                  only when the shortlist is worth it. Tools (CV score &amp; job fit) stay free.
                </p>
                <div className="mt-3 flex flex-wrap gap-2">
                  <button
                    type="button"
                    onClick={() => navigate('matches')}
                    className="rounded-md bg-navy-900 px-3 py-2 text-sm font-medium text-white hover:bg-navy-800"
                  >
                    See my matches →
                  </button>
                  <button
                    type="button"
                    onClick={() => navigate('tools')}
                    className="rounded-md border border-gray-300 bg-white px-3 py-2 text-sm font-medium text-gray-800 hover:bg-gray-50 dark:border-navy-600 dark:bg-navy-900 dark:text-gray-200"
                  >
                    Open free tools
                  </button>
                  <button
                    type="button"
                    onClick={() => navigate('billing')}
                    className="rounded-md border border-transparent px-3 py-2 text-sm font-medium text-accent-700 underline-offset-2 hover:underline dark:text-accent-400"
                  >
                    View plans
                  </button>
                </div>
              </div>
            )}
            {activeSection === 'overview' && (
              <ErrorBoundary>
                {/* Free and paid users both see value counters — proof before pay. */}
                <StatsRow />
                <div className="mt-6">
                  <OverviewPanel
                    freeProof={!isActive}
                    queued={sub?.queued_matches}
                    delivered={sub?.delivered_this_week}
                    onGoMatches={() => navigate('matches')}
                    onGoTools={() => navigate('tools')}
                    onGoBilling={() => navigate('billing')}
                  />
                </div>
              </ErrorBoundary>
            )}
            {activeSection === 'feed' && (
              <ErrorBoundary>
                {/* Agent card only when a real assignment exists (never invent white-glove). */}
                {plan === 'managed' && sub?.agent?.email && <AgentCard agent={sub.agent} />}
                <OpportunitiesFeed />
              </ErrorBoundary>
            )}
            {activeSection === 'matches' && (
              <ErrorBoundary>
                <MatchesPanel
                  plan={plan ?? 'starter'}
                  freeProof={!isActive}
                  queued={sub?.queued_matches ?? null}
                  delivered={sub?.delivered_this_week ?? null}
                  subQueryError={subQ.isError}
                  onUpgrade={() => navigate('billing')}
                />
              </ErrorBoundary>
            )}
            {activeSection === 'tools' && (
              <ErrorBoundary>
                <ToolsPanel />
              </ErrorBoundary>
            )}
            {activeSection === 'saved' && (
              <ErrorBoundary>
                <SavedJobsPanel />
              </ErrorBoundary>
            )}
            {activeSection === 'applications' && (
              <ErrorBoundary>
                <ApplicationsPanel />
              </ErrorBoundary>
            )}
            {activeSection === 'preferences' && (
              <ErrorBoundary>
                <PreferencesPanel />
              </ErrorBoundary>
            )}
            {activeSection === 'billing' && (
              <ErrorBoundary>
                {isActive && plan ? (
                  <BillingPanel
                    plan={plan}
                    renewsAt={sub?.renews_at}
                    cancelAtPeriodEnd={sub?.cancel_at_period_end}
                    onOpenPlanChange={() => setShowPlanChange(true)}
                    onOpenCancel={() => setShowCancel(true)}
                    t={t}
                  />
                ) : (
                  <CompletePaymentPanel plan={plan} status={subscription} />
                )}
              </ErrorBoundary>
            )}
            {activeSection === 'settings' && (
              <ErrorBoundary>
                <SettingsPage t={t} />
              </ErrorBoundary>
            )}
          </section>
        </div>

        {/* Mobile profile + completeness — below md */}
        <div className="mt-8 space-y-4 md:hidden">
          <ProfileCompleteness />
          <ProfileMount />
        </div>

        {showTour && <GuidedTour onDismiss={() => setShowTour(false)} />}

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
    </PreferenceChatHost>
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
      <Button variant="primary" size="md" type="button" onClick={() => void onSignIn()}>
        {t('nav.signIn')}
      </Button>
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
