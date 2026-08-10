import { useEffect, useState, useCallback } from 'react';
import { useAuth } from '@/providers/AuthProvider';
import { useSubscription } from '@/hooks/useSubscription';
import { normalizePlan } from '@/utils/plans';
import { Button } from '@/components/ui/Button';
import { DashboardHeader } from '@/components/dashboard/DashboardHeader';
import { AgentCard } from '@/components/dashboard/AgentCard';
import { BillingPanel } from '@/components/dashboard/BillingPanel';
import { SavedJobsPanel } from '@/components/dashboard/SavedJobsPanel';
import { ApplicationsPanel } from '@/components/dashboard/ApplicationsPanel';
import { CompletePaymentPanel } from '@/components/dashboard/CompletePaymentPanel';
import { PendingCheckoutPoller } from '@/components/dashboard/PendingCheckoutPoller';
import { MatchesPanel } from '@/components/dashboard/MatchesPanel';
import { CVPanel } from '@/components/dashboard/CVPanel';
import { DashboardSidebar, type SectionId } from '@/components/dashboard/DashboardSidebar';
import { DashboardMobileNav } from '@/components/dashboard/DashboardMobileNav';
import { PlanChangeModal } from '@/components/dashboard/PlanChangeModal';
import { CancelSubscriptionModal } from '@/components/dashboard/CancelSubscriptionModal';
import { SettingsPage, type SettingsTab } from '@/components/settings/SettingsPage';
import { ErrorBoundary } from '@/components/common/ErrorBoundary';
import { PreferenceChatHost } from '@/components/preference-chat';
import { useI18n } from '@/i18n/I18nProvider';
import { useDocumentTitle } from '@/hooks/useDocumentTitle';
import { useMatchingProfileGate } from '@/hooks/useMatchingProfileGate';
import { useSubscriptionGate } from '@/hooks/useSubscriptionGate';
import { useUserContext } from '@/hooks/useUserContext';
import { UserStageBanner } from '@/components/UserStageBanner';

/** Map legacy hashes and query to canonical section + optional settings tab. */
function resolveRoute(): { section: SectionId; settingsTab?: SettingsTab } {
  const hash = window.location.hash.replace('#', '').split('?')[0] ?? '';
  const params = new URLSearchParams(window.location.search);

  if (hash === 'billing' || params.get('tab') === 'subscription') {
    return { section: 'settings', settingsTab: 'subscription' };
  }
  if (hash === 'tools' || hash === 'preferences') {
    return { section: 'cv' };
  }
  if (hash === 'feed' || hash === 'overview' || !hash) {
    return { section: 'matches' };
  }
  const valid: SectionId[] = ['matches', 'cv', 'saved', 'applications', 'settings'];
  if (valid.includes(hash as SectionId)) {
    return { section: hash as SectionId };
  }
  return { section: 'matches' };
}

export default function Dashboard() {
  const { hasSession, ready, login } = useAuth();
  const { t } = useI18n();
  const initial =
    typeof window !== 'undefined' ? resolveRoute() : { section: 'matches' as SectionId };
  const [activeSection, setActiveSection] = useState<SectionId>(initial.section);
  const [settingsTab, setSettingsTab] = useState<SettingsTab | undefined>(initial.settingsTab);
  const [showPlanChange, setShowPlanChange] = useState(false);
  const [showCancel, setShowCancel] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);

  const subQ = useSubscription();
  // Subscription first: never paint product UI or load profile until allowed.
  const subscriptionGate = useSubscriptionGate();
  const profileGate = useMatchingProfileGate({ enabled: subscriptionGate.allowed });
  // Full journey stage (subscription + readiness) for banners / data attributes.
  const userCtx = useUserContext({ loadProfile: subscriptionGate.allowed });

  const sectionLabels: Record<string, string> = {
    matches: 'Matches',
    cv: 'CV',
    saved: 'Saved',
    applications: 'Applications',
    settings: 'Settings',
  };
  useDocumentTitle(`${sectionLabels[activeSection] ?? 'Dashboard'} | Stawi`);

  useEffect(() => {
    const onHashChange = () => {
      const r = resolveRoute();
      setActiveSection(r.section);
      setSettingsTab(r.settingsTab);
    };
    window.addEventListener('hashchange', onHashChange);
    return () => window.removeEventListener('hashchange', onHashChange);
  }, []);

  const navigate = (id: SectionId) => {
    // Canonicalize legacy ids if anything still routes them.
    let next: SectionId = id;
    if (id === 'tools' || id === 'preferences') next = 'cv';
    if (id === 'billing') next = 'settings';
    if (id === 'feed' || id === 'overview') next = 'matches';

    if (next === 'settings' && id === 'billing') {
      setSettingsTab('subscription');
    } else if (next !== 'settings') {
      setSettingsTab(undefined);
    }
    window.location.hash = next;
    setActiveSection(next);
    // Mobile: jump to content top after tab change (bottom nav).
    if (typeof window !== 'undefined' && window.matchMedia('(max-width: 767px)').matches) {
      window.scrollTo({ top: 0, behavior: 'smooth' });
    }
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

  // Product UI only after billing entitlement is active (GET /me/subscription).
  if (!subscriptionGate.allowed) {
    if (subscriptionGate.confirmingPayment) {
      return <PaymentConfirmingShell />;
    }
    if (subscriptionGate.error) {
      return (
        <SubscriptionVerifyError
          onRetry={() => {
            void subQ.refetch();
          }}
        />
      );
    }
    return <ProfileGateSkeleton />;
  }

  // Wait only for profile readiness fetch — incomplete profiles stay here
  // (CV hub). Never redirect paid users back to onboarding (redirect loop).
  if (profileGate.checking) {
    return <ProfileGateSkeleton />;
  }

  const sub = subQ.data;
  const plan = normalizePlan(sub?.plan ?? null);
  // Gate already required entitled status (active | past_due).
  const isActive = sub?.status === 'active' || sub?.status === 'past_due';
  const subscription = sub?.status ?? 'none';
  const subscriptionPanel =
    isActive && plan ? (
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
    );

  return (
    <PreferenceChatHost>
      <div
        className="mx-auto max-w-6xl px-4 py-5 sm:px-6 sm:py-8 md:pb-10 lg:px-8"
        data-user-stage={userCtx.stage}
      >
        <DashboardHeader
          plan={plan}
          status={subscription}
          stageLabel={userCtx.label}
          stageId={userCtx.stage}
          onOpenMenu={() => setMenuOpen(true)}
        />
        <div className="mt-4 empty:hidden">
          <UserStageBanner stage={userCtx} />
        </div>
        <PendingCheckoutPoller />
        <div className="mt-6 grid gap-8 lg:grid-cols-[13.5rem_1fr] lg:gap-10">
          <aside className="hidden md:block">
            <DashboardSidebar
              active={activeSection}
              onNavigate={navigate}
              t={t}
              matchCount={sub?.queued_matches}
            />
          </aside>
          <section className="min-w-0">
            {activeSection === 'matches' && (
              <ErrorBoundary>
                {plan === 'managed' && sub?.agent?.email && <AgentCard agent={sub.agent} />}
                <MatchesPanel
                  plan={plan ?? 'starter'}
                  freeProof={!isActive}
                  queued={sub?.queued_matches ?? null}
                  delivered={sub?.delivered_this_week ?? null}
                  subQueryError={subQ.isError}
                  subLoading={subQ.isLoading && sub == null}
                  // Matches is match-only: CV upload probes only when no CV.
                  // Preference gaps (salary, countries) stay on the CV hub.
                  cvPresent={userCtx.readiness?.matchCapable ?? true}
                  preferenceMissing={userCtx.readiness?.preferenceMissing ?? []}
                  onUpgrade={() => {
                    setSettingsTab('subscription');
                    navigate('settings');
                  }}
                />
              </ErrorBoundary>
            )}
            {activeSection === 'cv' && (
              <ErrorBoundary>
                <CVPanel />
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
            {activeSection === 'settings' && (
              <ErrorBoundary>
                <SettingsPage
                  t={t}
                  subscriptionPanel={subscriptionPanel}
                  initialTab={settingsTab ?? 'notifications'}
                />
              </ErrorBoundary>
            )}
          </section>
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
        <DashboardMobileNav
          open={menuOpen}
          onClose={() => setMenuOpen(false)}
          active={activeSection}
          onNavigate={navigate}
          t={t}
          matchCount={sub?.queued_matches}
        />
      </div>
    </PreferenceChatHost>
  );
}

function SignedOut({ onSignIn }: { onSignIn: () => Promise<void> }) {
  return (
    <div className="mx-auto max-w-sm px-4 py-20 text-center">
      <h1 className="text-2xl font-semibold tracking-tight text-main">Sign in</h1>
      <p className="mt-2 text-sm leading-relaxed text-secondary">
        Access your matches, CV tools, and application tracker.
      </p>
      <Button className="mt-8" variant="primary" onClick={() => void onSignIn()}>
        Sign in
      </Button>
    </div>
  );
}

function Skeleton() {
  return (
    <div className="mx-auto max-w-6xl animate-pulse px-4 py-8">
      <div className="h-8 w-40 rounded bg-surface-hover" />
      <div className="mt-8 h-64 rounded bg-surface-hover" />
    </div>
  );
}

function ProfileGateSkeleton() {
  return (
    <div className="mx-auto max-w-sm px-4 py-16 text-center">
      <div className="mx-auto h-8 w-48 animate-pulse rounded bg-surface-hover" />
      <p className="mt-4 text-sm text-secondary">Checking access…</p>
      <p className="mt-2 text-xs text-secondary">
        Confirming your subscription with billing before opening the dashboard.
      </p>
    </div>
  );
}

/** Checkout return only — polls billing until /me/subscription is active. */
function PaymentConfirmingShell() {
  return (
    <div className="mx-auto max-w-md px-4 py-16 text-center" data-user-stage="confirming_payment">
      <div className="mx-auto h-10 w-10 animate-spin rounded-full border-2 border-accent-500 border-t-transparent" />
      <p className="mt-6 text-xs font-semibold uppercase tracking-wide text-blue-700 dark:text-blue-300">
        Confirming payment
      </p>
      <h1 className="mt-1 text-lg font-semibold text-main">Activating your subscription</h1>
      <p className="mt-2 text-sm text-secondary">
        Waiting for billing to activate your plan. This usually takes under a minute. The dashboard
        opens only after confirmation.
      </p>
      <div className="mt-6 text-left">
        <PendingCheckoutPoller />
      </div>
      <p className="mt-8 text-xs text-secondary">
        <a href="/onboarding/" className="underline underline-offset-2">
          Back to setup
        </a>
      </p>
    </div>
  );
}

function SubscriptionVerifyError({ onRetry }: { onRetry: () => void }) {
  return (
    <div className="mx-auto max-w-sm px-4 py-16 text-center">
      <h1 className="text-lg font-semibold text-main">Couldn&apos;t verify subscription</h1>
      <p className="mt-2 text-sm text-secondary">
        The dashboard only opens after billing confirms an active subscription. Check your
        connection and try again.
      </p>
      <Button className="mt-6" variant="primary" onClick={onRetry}>
        Retry
      </Button>
      <p className="mt-4 text-xs text-secondary">
        <a href="/onboarding/" className="underline underline-offset-2">
          Continue setup / subscribe
        </a>
      </p>
    </div>
  );
}
