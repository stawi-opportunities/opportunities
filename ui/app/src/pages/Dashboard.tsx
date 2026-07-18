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
import { CompletePaymentPanel } from '@/components/dashboard/CompletePaymentPanel';
import { PendingCheckoutPoller } from '@/components/dashboard/PendingCheckoutPoller';
import { MatchesPanel } from '@/components/dashboard/MatchesPanel';
import { ToolsPanel } from '@/components/dashboard/ToolsPanel';
import { OpportunitiesFeed } from '@/components/OpportunitiesFeed';
import { DashboardSidebar, type SectionId } from '@/components/dashboard/DashboardSidebar';
import { PlanChangeModal } from '@/components/dashboard/PlanChangeModal';
import { CancelSubscriptionModal } from '@/components/dashboard/CancelSubscriptionModal';
import { SettingsPage } from '@/components/settings/SettingsPage';
import { ErrorBoundary } from '@/components/common/ErrorBoundary';
import { PreferenceChatHost } from '@/components/preference-chat';
import { useI18n } from '@/i18n/I18nProvider';
import { useDocumentTitle } from '@/hooks/useDocumentTitle';
import { useTheme } from '@/providers/ThemeProvider';

function getSectionFromHash(): SectionId {
  const hash = window.location.hash.replace('#', '');
  const valid: SectionId[] = [
    'matches',
    'feed',
    'tools',
    'saved',
    'applications',
    'preferences',
    'billing',
    'settings',
  ];
  // Legacy #overview → matches
  if (hash === 'overview' || !hash) return 'matches';
  return valid.includes(hash as SectionId) ? (hash as SectionId) : 'matches';
}

export default function Dashboard() {
  const { hasSession, ready, login } = useAuth();
  const { t } = useI18n();
  const [activeSection, setActiveSection] = useState<SectionId>(getSectionFromHash);
  const [showPlanChange, setShowPlanChange] = useState(false);
  const [showCancel, setShowCancel] = useState(false);

  const subQ = useSubscription();

  const sectionLabels: Record<string, string> = {
    matches: 'Matches',
    feed: 'Feed',
    tools: 'Tools',
    saved: 'Saved',
    applications: 'Applications',
    preferences: 'Preferences',
    billing: 'Billing',
    settings: 'Settings',
  };
  useDocumentTitle(`${sectionLabels[activeSection] ?? 'Dashboard'} | Stawi`);

  useEffect(() => {
    const onHashChange = () => setActiveSection(getSectionFromHash());
    window.addEventListener('hashchange', onHashChange);
    return () => window.removeEventListener('hashchange', onHashChange);
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

  if (!ready) return <Skeleton />;
  if (!hasSession) return <SignedOut onSignIn={login} />;

  const sub = subQ.data;
  const plan = normalizePlan(sub?.plan ?? null);
  const isActive =
    sub?.status === 'active' || sub?.status === 'past_due' || sub?.status === 'trial';
  const subscription = sub?.status ?? 'none';

  return (
    <PreferenceChatHost>
      <div className="mx-auto max-w-6xl px-4 py-6 sm:px-6 lg:px-8">
        <DashboardHeader
          plan={plan}
          status={subscription}
          onOpenPlanChange={() => setShowPlanChange(true)}
          t={t}
        />
        <PendingCheckoutPoller />

        <div className="mt-4 md:hidden">
          <DashboardSidebar
            active={activeSection}
            onNavigate={navigate}
            t={t}
            matchCount={sub?.queued_matches}
          />
        </div>

        <div className="mt-6 grid gap-6 lg:grid-cols-[200px_1fr]">
          <aside className="hidden md:block">
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
          <section>
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
            {activeSection === 'feed' && (
              <ErrorBoundary>
                {plan === 'managed' && sub?.agent?.email && <AgentCard agent={sub.agent} />}
                <OpportunitiesFeed />
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

        <div className="mt-8 md:hidden">
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
    } catch {
      // best-effort
    }
    return () => handle?.unmount();
  }, [resolvedTheme]);
  return <div ref={hostRef} className="min-h-[200px]" />;
}

function SignedOut({ onSignIn }: { onSignIn: () => Promise<void> }) {
  return (
    <div className="mx-auto max-w-sm py-16 text-center">
      <h1 className="text-xl font-semibold text-gray-900 dark:text-white">Sign in</h1>
      <p className="mt-2 text-sm text-gray-600 dark:text-gray-300">Access matches and tools.</p>
      <Button className="mt-6" variant="primary" onClick={() => void onSignIn()}>
        Sign in
      </Button>
    </div>
  );
}

function Skeleton() {
  return (
    <div className="mx-auto max-w-6xl animate-pulse px-4 py-8">
      <div className="h-8 w-40 rounded bg-gray-100" />
      <div className="mt-8 h-64 rounded bg-gray-100" />
    </div>
  );
}
