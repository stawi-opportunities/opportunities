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
import { MatchesPanel } from "@/components/dashboard/MatchesPanel";
import { SavedJobsPanel } from "@/components/dashboard/SavedJobsPanel";
import { ApplicationsPanel } from "@/components/dashboard/ApplicationsPanel";
import { BillingPanel } from "@/components/dashboard/BillingPanel";
import { PreferencesPanel } from "@/components/dashboard/PreferencesPanel";
import { CompletePaymentPanel } from "@/components/dashboard/CompletePaymentPanel";
import { PendingCheckoutPoller } from "@/components/dashboard/PendingCheckoutPoller";
import { useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { mount as mountProfile, type MountHandle } from '@stawi/profile';
import { authRuntime } from '@/auth/runtime';
import { profileWidgetTokens, profileWidgetCSS } from '@/theme/profile-widget';
import { useAuth } from '@/providers/AuthProvider';
import { getConfig } from '@/utils/config';
import { fetchMeSubscription, createCheckout, pollCheckoutStatus } from '@/api/candidates';
import { OpportunitiesFeed } from '@/components/OpportunitiesFeed';
import { normalizePlan, planById, type PlanId } from '@/utils/plans';
import { OnboardingRouter } from '@/onboarding/router';
import { useI18n } from '@/i18n/I18nProvider';

const PENDING_PROMPT_KEY = 'stawi.billing.pending_prompt_id';

const PREFERENCE_KINDS: ReadonlyArray<{
  kind: string;
  flow: string;
  labelKey: import('@/i18n/strings').StringKey;
}> = [
  { kind: 'job', flow: 'job-onboarding-v1', labelKey: 'kind.job' },
  { kind: 'scholarship', flow: 'scholarship-onboarding-v1', labelKey: 'kind.scholarship' },
  { kind: 'tender', flow: 'tender-onboarding-v1', labelKey: 'kind.tender' },
  { kind: 'deal', flow: 'deal-onboarding-v1', labelKey: 'kind.deal' },
  { kind: 'funding', flow: 'funding-onboarding-v1', labelKey: 'kind.funding' },
];

export default function Dashboard() {
  const { state, login } = useAuth();

  const subQ = useSubscription();
  const subQ = useQuery({
    queryKey: ['me-subscription'],
    queryFn: fetchMeSubscription,
    enabled: state === 'authenticated',
    staleTime: 60_000,
  });

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

function DashboardHeader({ plan, active }: { plan: PlanId | null; active: boolean }) {
  const { t } = useI18n();
  const label = plan && active ? planById(plan).name : t('dash.setupIncomplete');
  const tagline = plan && active ? planById(plan).tagline : t('dash.finishPayment');
  return (
    <header className="flex flex-wrap items-end justify-between gap-4">
      <div>
        <h1 className="text-3xl font-bold text-gray-900">{t('dash.title')}</h1>
        <p className="mt-1 flex items-center gap-2 text-gray-600">
          <span
            className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${
              plan && active ? 'bg-emerald-50 text-emerald-700' : 'bg-amber-50 text-amber-700'
            }`}
          >
            {label}
          </span>
          <span>{tagline}</span>
        </p>
      </div>
      <div className="flex items-center gap-3">
        <a href="/jobs/" className="text-sm font-medium text-gray-700 hover:text-navy-900">
          {t('dash.browseJobs')}
        </a>
        {plan !== 'managed' && (
          <a
            href="/pricing/"
            className="inline-flex items-center rounded-md bg-navy-900 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-navy-800"
          >
            {active ? t('dash.changePlan') : t('dash.viewPlans')}
          </a>
        )}
      </div>
    </header>
  );
}

function CompletePaymentPanel({ plan, status }: { plan: PlanId | null; status: string }) {
  const { t } = useI18n();
  const headline =
    status === 'past_due'
      ? t('dash.paymentPastDue')
      : status === 'cancelled'
        ? t('dash.subCancelled')
        : plan
          ? t('dash.finishSetup').replace('{plan}', planById(plan).name)
          : t('dash.choosePlan');
  const body =
    status === 'past_due'
      ? t('dash.updatePayment')
      : status === 'cancelled'
        ? t('dash.reactivateHint')
        : t('dash.matchingHint');
  return (
    <div className="rounded-lg border border-amber-300 bg-amber-50 p-6">
      <p className="text-xs font-semibold uppercase tracking-wide text-amber-700">
        {t('dash.actionNeeded')}
      </p>
      <h2 className="mt-2 text-xl font-bold text-gray-900">{headline}</h2>
      <p className="mt-1 text-sm text-gray-700">{body}</p>
      <div className="mt-4 flex flex-wrap gap-3">
        {plan && status !== 'cancelled' ? (
          <RetryCheckoutButton plan={plan} />
        ) : (
          <a
            href="/pricing/"
            className="inline-flex items-center rounded-md bg-navy-900 px-4 py-2 text-sm font-semibold text-white hover:bg-navy-800"
          >
            {t('dash.choosePlan')}
          </a>
        )}
        <a
          href="/onboarding/"
          className="inline-flex items-center rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
        >
          {t('dash.editPreferences')}
        </a>
      </div>
    </div>
  );
}

function RetryCheckoutButton({ plan }: { plan: PlanId }) {
  const { t } = useI18n();
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const info = planById(plan);

  const go = async () => {
    setBusy(true);
    setErr(null);
    try {
      const res = await createCheckout({ plan_id: plan });
      if (res.status === 'redirect' && res.redirect_url) {
        window.location.href = res.redirect_url;
        return;
      }
      if (res.status === 'pending' && res.prompt_id) {
        window.location.href = `/dashboard/?billing=pending&prompt_id=${encodeURIComponent(res.prompt_id)}`;
        return;
      }
      if (res.status === 'paid') {
        window.location.href = '/dashboard/?billing=success';
        return;
      }
      throw new Error(res.error || 'Checkout did not complete.');
    } catch (e) {
      setErr(e instanceof Error ? e.message : 'Checkout failed. Please try again.');
      setBusy(false);
    }
  };

  return (
    <div>
      <button
        type="button"
        onClick={() => void go()}
        disabled={busy}
        className="inline-flex items-center rounded-md bg-navy-900 px-4 py-2 text-sm font-semibold text-white hover:bg-navy-800 disabled:opacity-60"
      >
        {busy
          ? t('dash.openingPayment')
          : `${t('dash.payPerMonth')} $${info.price}${t('dash.perMonth')}`}
      </button>
      {err && <p className="mt-2 text-xs text-red-700">{err}</p>}
    </div>
  );
}

function AgentCard({ agent }: { agent: { name: string; email: string } }) {
  const { t } = useI18n();
  return (
    <div className="rounded-lg border border-accent-200 bg-accent-50 p-6">
      <p className="text-xs font-semibold uppercase tracking-wide text-accent-700">
        {t('dash.yourAgent')}
      </p>
      <h2 className="mt-2 text-xl font-bold text-gray-900">{agent.name}</h2>
      <p className="mt-1 text-sm text-gray-700">{t('dash.agentHint')}</p>
      <div className="mt-4 flex flex-wrap gap-3">
        <a
          href={`mailto:${agent.email}`}
          className="inline-flex items-center rounded-md bg-navy-900 px-4 py-2 text-sm font-semibold text-white hover:bg-navy-800"
        >
          {t('dash.emailAgent')} {agent.name.split(' ')[0]}
        </a>
        <a
          href="#schedule"
          className="inline-flex items-center rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50"
        >
          {t('dash.scheduleCall')}
        </a>
      </div>
    </div>
  );
}

function PendingCheckoutPoller() {
  const { t } = useI18n();
  const qc = useQueryClient();
  const [state, setState] = useState<'idle' | 'polling' | 'paid' | 'failed'>('idle');
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const urlPromptId = params.get('prompt_id');
    const stashed = (() => {
      try {
        return localStorage.getItem(PENDING_PROMPT_KEY);
      } catch {
        return null;
      }
    })();
    const promptId = urlPromptId ?? stashed;
    if (!promptId) return;

    if (urlPromptId && urlPromptId !== stashed) {
      try {
        localStorage.setItem(PENDING_PROMPT_KEY, urlPromptId);
      } catch {
        /* private mode */
      }
    }

    let cancelled = false;
    setState('polling');
    const start = Date.now();
    const MAX_MS = 3 * 60 * 1000;
    const INTERVAL_MS = 4_000;

    const tick = async () => {
      if (cancelled) return;
      try {
        const res = await pollCheckoutStatus(promptId);
        if (cancelled) return;
        if (res.status === 'paid') {
          try {
            localStorage.removeItem(PENDING_PROMPT_KEY);
          } catch {
            /* ignore */
          }
          await qc.invalidateQueries({ queryKey: ['me-subscription'] });
          setState('paid');
          const u = new URL(window.location.href);
          u.searchParams.delete('prompt_id');
          u.searchParams.set('billing', 'success');
          window.history.replaceState(null, '', u.toString());
          return;
        }
        if (res.status === 'failed') {
          try {
            localStorage.removeItem(PENDING_PROMPT_KEY);
          } catch {
            /* ignore */
          }
          setError(res.error || t('dash.paymentFailed'));
          setState('failed');
          const u = new URL(window.location.href);
          u.searchParams.delete('prompt_id');
          u.searchParams.set('billing', 'failed');
          window.history.replaceState(null, '', u.toString());
          return;
        }
      } catch {
        // Transient — keep polling until the budget expires.
      }
      if (Date.now() - start > MAX_MS) {
        setError(t('dash.paymentFailed'));
        setState('failed');
        return;
      }
      setTimeout(tick, INTERVAL_MS);
    };
    void tick();
    return () => {
      cancelled = true;
    };
  }, [qc, t]);

  if (state === 'idle') return null;
  if (state === 'paid') {
    return (
      <div className="mt-4 rounded-md border border-emerald-200 bg-emerald-50 p-4 text-sm text-emerald-800">
        {t('dash.paymentReceived')}
      </div>
    );
  }
  if (state === 'failed') {
    return (
      <div className="mt-4 rounded-md border border-amber-300 bg-amber-50 p-4 text-sm text-amber-800">
        {error ?? t('dash.paymentFailed')}
      </div>
    );
  }
  return (
    <div
      className="mt-4 flex items-center gap-3 rounded-md border border-blue-200 bg-blue-50 p-4 text-sm text-blue-800"
      role="status"
      aria-live="polite"
    >
      <div className="h-4 w-4 animate-spin rounded-full border-2 border-blue-600 border-t-transparent" />
      {t('dash.paymentWaiting')}
    </div>
  );
}

function PreferencesPanel() {
  const { t } = useI18n();
  const [active, setActive] = useState<string>(PREFERENCE_KINDS[0]!.kind);
  const [status, setStatus] = useState<'idle' | 'saving' | 'saved' | 'error'>('idle');
  const [errMsg, setErrMsg] = useState<string | null>(null);
  const [enabledKinds, setEnabledKinds] = useState<string[] | null>(null);

  useEffect(() => {
    let cancelled = false;
    authRuntime()
      .fetch<{ enabled_kinds?: string[] }>('/matching/candidates/match-kinds')
      .then((data) => {
        if (cancelled) return;
        setEnabledKinds(data.enabled_kinds ?? ['job', 'scholarship']);
      })
      .catch(() => {
        if (cancelled) return;
        setEnabledKinds(['job', 'scholarship']);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const visibleKinds =
    enabledKinds === null
      ? PREFERENCE_KINDS
      : PREFERENCE_KINDS.filter((k) => enabledKinds.includes(k.kind));

  useEffect(() => {
    if (visibleKinds.length === 0) return;
    if (!visibleKinds.some((k) => k.kind === active)) {
      setActive(visibleKinds[0]!.kind);
    }
  }, [visibleKinds, active]);

  const activeEntry =
    visibleKinds.find((k) => k.kind === active) ?? visibleKinds[0] ?? PREFERENCE_KINDS[0]!;

  async function persist(kind: string, prefs: unknown) {
    setStatus('saving');
    setErrMsg(null);
    try {
      await authRuntime().fetch('/matching/candidates/preferences', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ opt_ins: { [kind]: prefs } }),
      });
      setStatus('saved');
    } catch (e) {
      setStatus('error');
      setErrMsg(e instanceof Error ? e.message : t('dash.preferencesFailed'));
    }
  }

  return (
    <Panel title={t('dash.matchPreferences')}>
      <p className="text-sm text-gray-600">{t('dash.matchPreferencesHint')}</p>
      <nav
        className="mt-4 flex flex-wrap gap-1 border-b border-gray-200"
        role="tablist"
        aria-label="Opportunity kinds"
      >
        {visibleKinds.map(({ kind, labelKey }) => {
          const on = active === kind;
          return (
            <button
              key={kind}
              type="button"
              role="tab"
              aria-selected={on}
              className={`px-4 py-2 text-sm font-medium transition-colors ${
                on
                  ? 'border-b-2 border-accent-500 text-navy-900'
                  : 'border-b-2 border-transparent text-gray-600 hover:text-gray-900'
              }`}
              onClick={() => {
                setActive(kind);
                setStatus('idle');
                setErrMsg(null);
              }}
            >
              {t(labelKey)}
            </button>
          );
        })}
      </nav>
      <div className="mt-6">
        <OnboardingRouter
          flowId={activeEntry.flow}
          onSubmit={(prefs) => void persist(activeEntry.kind, prefs)}
        />
      </div>
      {status === 'saving' && <p className="mt-3 text-sm text-gray-500">{t('dash.saving')}</p>}
      {status === 'saved' && (
        <p className="mt-3 text-sm text-emerald-700">{t('dash.preferencesSaved')}</p>
      )}
      {status === 'error' && (
        <p className="mt-3 text-sm text-red-700" role="alert">
          {errMsg ?? t('dash.preferencesFailed')}
        </p>
      )}
    </Panel>
  );
}

function BillingPanel({ plan, renewsAt }: { plan: PlanId; renewsAt?: string }) {
  const { t } = useI18n();
  const info = planById(plan);
  return (
    <Panel title={t('dash.billing')}>
      <p className="text-sm text-gray-700">
        <span className="font-medium">{info.name}</span> · ${info.price}
        {t('dash.perMonth')} · <span className="text-emerald-700">{t('dash.active')}</span>
      </p>
      {renewsAt && (
        <p className="mt-1 text-xs text-gray-500">
          {t('dash.renewsOn')} {new Date(renewsAt).toLocaleDateString()}
        </p>
      )}
      <a
        href="/pricing/"
        className="mt-3 inline-block text-sm font-medium text-accent-600 hover:text-accent-700"
      >
        {t('dash.changePlanOrCancel')}
      </a>
    </Panel>
  );
}

function Panel({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="rounded-lg border border-gray-200 bg-white p-6">
      <h2 className="text-lg font-semibold text-gray-900">{title}</h2>
      <div className="mt-2">{children}</div>
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
