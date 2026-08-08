import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useAuth } from '@/providers/AuthProvider';
import { fetchMeCV, submitOnboarding } from '@/api/profile';
import { PLANS, planById, type PlanId } from '@/utils/plans';
import { startCheckoutAndNavigate } from '@/utils/checkout';
import { useI18n } from '@/i18n/I18nProvider';
import {
  fetchMeSubscription,
  fetchOnboardingDraft,
  saveOnboardingDraft,
  type OnboardingChatFields,
  type OnboardingChatMessage,
} from '@/api/candidates';
import {
  PreferenceChat,
  draftToChatFields,
  fieldsToDraft,
  summaryChips,
} from '@/components/preference-chat';
import { filterPlacementMessages } from '@/utils/chatDisplay';
import { evaluateProfileReadiness, mergeCVIntoFields } from '@/utils/profileReadiness';
import { isChatReady } from '@/onboarding/chatHeuristic';

type Phase = 'chat' | 'plan';

function readPlanFromQuery(): PlanId {
  if (typeof window === 'undefined') return 'starter';
  const p = new URL(window.location.href).searchParams.get('plan');
  if (p === 'starter' || p === 'managed') return p;
  if (p === 'pro') return 'managed';
  return 'starter';
}

function isPaidStatus(status: string | undefined): boolean {
  // Match useSubscriptionGate: active or past_due (still entitled).
  return status === 'active' || status === 'past_due';
}

/**
 * Funnel: sign-in → intake chat (auto-advances when ready) → compact paywall
 * (one-tap plan → checkout). No free-tier exit; subscription required.
 */
export default function Onboarding() {
  const { t } = useI18n();
  const { state, hasSession, ready, login } = useAuth();
  const wasAuthenticated = useRef(hasSession);

  const subQ = useQuery({
    queryKey: ['me-subscription'],
    queryFn: fetchMeSubscription,
    enabled: hasSession,
    staleTime: 60_000,
    placeholderData: (prev) => prev,
  });

  // Already subscribed → always leave the paywall funnel. Profile gaps are
  // finished on the dashboard (CV hub), never by re-paying.
  useEffect(() => {
    if (!hasSession || subQ.isLoading || subQ.isError) return;
    if (!isPaidStatus(subQ.data?.status)) return;
    window.location.replace('/dashboard/');
  }, [hasSession, subQ.isLoading, subQ.isError, subQ.data?.status]);

  useEffect(() => {
    if (hasSession) {
      wasAuthenticated.current = true;
      return;
    }
    if (!ready || state !== 'unauthenticated') return;
    if (wasAuthenticated.current) {
      window.location.replace('/');
    }
  }, [hasSession, ready, state]);

  const [loginBusy, setLoginBusy] = useState(false);
  const [loginError, setLoginError] = useState<string | null>(null);

  async function onSignIn() {
    if (loginBusy) return;
    setLoginError(null);
    setLoginBusy(true);
    try {
      await login();
    } catch {
      setLoginError('Could not start sign-in. Try again in a moment.');
      setLoginBusy(false);
    }
  }

  const [phase, setPhase] = useState<Phase>('chat');
  const [fields, setFields] = useState<OnboardingChatFields>({});
  const [messages, setMessages] = useState<OnboardingChatMessage[]>([]);
  const [plan, setPlan] = useState<PlanId>(readPlanFromQuery);
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [draftLoaded, setDraftLoaded] = useState(false);
  const [cvOnFile, setCvOnFile] = useState(false);
  const wizardStepRef = useRef<1 | 2 | 3>(1);
  const [chatSession, setChatSession] = useState(0);
  /** Prevent double auto-advance chat → plan. */
  const advancedToPlanRef = useRef(false);

  function bumpWizardStep(min: 1 | 2 | 3): 1 | 2 | 3 {
    const next = (wizardStepRef.current > min ? wizardStepRef.current : min) as 1 | 2 | 3;
    wizardStepRef.current = next;
    return next;
  }

  // Resume draft: skip chat when profile already complete or wizard past chat.
  useEffect(() => {
    if (!hasSession || draftLoaded) return;
    let cancelled = false;
    (async () => {
      const [draft, cv] = await Promise.all([fetchOnboardingDraft(), fetchMeCV()]);
      if (cancelled) return;

      let f = draftToChatFields(draft.fields);
      let msgs = filterPlacementMessages(draft.messages ?? []);
      f = mergeCVIntoFields(f, cv);

      if (cv?.present) {
        setCvOnFile(true);
      }

      const readiness = evaluateProfileReadiness(cv, draft.fields);
      const step = (draft.step === 2 || draft.step === 3 ? draft.step : 1) as 1 | 2 | 3;
      wizardStepRef.current = step;

      const skipChat =
        step >= 2 || readiness.ready || (Boolean(cv?.placement_ready) && isChatReady(f));

      if (skipChat) {
        if (msgs.length === 0) {
          msgs = [
            {
              role: 'assistant',
              content: 'Your profile is ready. Choose a plan below to start matching.',
            },
          ];
        }
        advancedToPlanRef.current = true;
        setPhase('plan');
        bumpWizardStep(2);
      }

      setFields(f);
      setMessages(msgs);
      if (draft.fields.plan === 'starter' || draft.fields.plan === 'managed') {
        setPlan(draft.fields.plan);
      }
      setDraftLoaded(true);
    })();
    return () => {
      cancelled = true;
    };
  }, [hasSession, draftLoaded]);

  const goToPlan = useCallback(
    async (f: OnboardingChatFields, msgs: OnboardingChatMessage[]) => {
      setFields(f);
      setMessages(msgs);
      if (phase === 'plan') return;
      advancedToPlanRef.current = true;
      setPhase('plan');
      const step = bumpWizardStep(2);
      try {
        await saveOnboardingDraft(step, fieldsToDraft(f, plan), msgs);
      } catch {
        /* non-blocking */
      }
    },
    [phase, plan]
  );

  function openChat() {
    advancedToPlanRef.current = false;
    setChatSession((n) => n + 1);
    setPhase('chat');
  }

  const chips = useMemo(() => summaryChips(fields), [fields]);

  function profilePayload(selected: PlanId) {
    const opportunityCountries = fields.preferred_countries?.length
      ? fields.preferred_countries
      : fields.country
        ? [fields.country]
        : [];
    return {
      target_job_title: fields.target_job_title?.trim() || '',
      experience_level: fields.experience_level?.trim() || '',
      job_search_status: fields.job_search_status ?? 'actively_looking',
      salary_min: fields.salary_min ?? undefined,
      salary_max: fields.salary_max ?? fields.salary_min ?? undefined,
      currency: fields.currency ?? 'USD',
      wants_ats_report: true,
      preferred_regions: fields.preferred_regions ?? [],
      preferred_timezones: fields.preferred_timezones ?? [],
      preferred_languages: fields.preferred_languages ?? [],
      job_types: fields.job_types ?? [],
      country: opportunityCountries[0] ?? fields.country ?? '',
      plan: selected,
      agree_terms: true as const,
    };
  }

  /** One-tap: select plan, save profile, open Flutterwave. */
  async function checkoutPlan(selected: PlanId) {
    if (submitting) return;
    setPlan(selected);
    setSubmitting(true);
    setSubmitError(null);
    try {
      const payStep = bumpWizardStep(3);
      await saveOnboardingDraft(payStep, fieldsToDraft(fields, selected), messages).catch(
        () => undefined
      );
      await submitOnboarding(profilePayload(selected)).catch(() => undefined);
      await startCheckoutAndNavigate({ plan_id: selected });
    } catch (e) {
      setSubmitError(e instanceof Error && e.message ? e.message : t('error.somethingWrong'));
      setSubmitting(false);
    }
  }

  if (!ready) {
    return (
      <div className="flex min-h-[40vh] items-center justify-center text-sm text-gray-400">
        Loading…
      </div>
    );
  }

  if (!hasSession) {
    return (
      <div className="mx-auto flex max-w-sm flex-col items-center px-4 py-20 text-center">
        <h1 className="text-xl font-semibold text-gray-900 dark:text-white">Sign in to continue</h1>
        <p className="mt-2 text-sm text-gray-600 dark:text-stone-300">
          Secure sign-in — then a short profile chat and subscribe to unlock matches.
        </p>
        <button
          type="button"
          onClick={() => void onSignIn()}
          disabled={loginBusy}
          className="mt-6 inline-flex items-center rounded-full bg-navy-900 px-6 py-3 text-sm font-semibold text-white hover:bg-navy-800 disabled:opacity-70"
        >
          {loginBusy ? 'Signing in…' : 'Sign in'}
        </button>
        {loginError && (
          <p className="mt-3 text-sm text-red-600" role="alert">
            {loginError}
          </p>
        )}
        <a href="/search/" className="mt-4 text-sm text-gray-500 hover:underline">
          Browse jobs instead
        </a>
      </div>
    );
  }

  if (phase === 'chat') {
    return (
      <div className="flex min-h-[min(100dvh,40rem)] flex-col bg-stone-50/80 dark:bg-navy-950">
        {draftLoaded ? (
          <PreferenceChat
            key={chatSession}
            mode="intake"
            initialFields={fields}
            initialMessages={messages}
            plan={plan}
            cvOnFile={cvOnFile}
            showCompleteAction
            completeLabel="Continue to subscribe"
            className="flex min-h-0 flex-1 flex-col"
            onFieldsChange={(f, meta) => {
              setFields(f);
              setMessages(meta.messages);
              const step = bumpWizardStep(meta.ready ? 2 : 1);
              void saveOnboardingDraft(step, fieldsToDraft(f, plan), meta.messages).catch(
                () => undefined
              );
              // Parent-level advance: survives chat remounts and missed autoComplete.
              if (meta.ready) {
                void goToPlan(f, meta.messages);
              }
            }}
            onComplete={(f, meta) => void goToPlan(f, meta?.messages ?? messages)}
          />
        ) : (
          <div className="flex flex-1 items-center justify-center text-sm text-stone-400">
            Loading…
          </div>
        )}
      </div>
    );
  }

  // ── Compact paywall ───────────────────────────────────────────────────
  return (
    <div className="mx-auto flex w-full max-w-lg flex-col px-4 py-10 sm:px-6">
      <header className="mb-6 text-center">
        <p className="text-xs font-semibold uppercase tracking-wide text-emerald-700 dark:text-emerald-400">
          Profile ready
        </p>
        <h1 className="mt-1 text-2xl font-bold tracking-tight text-gray-900 dark:text-white sm:text-3xl">
          Subscribe to unlock matches
        </h1>
        <p className="mt-2 text-sm text-gray-600 dark:text-stone-300">
          One tap opens secure checkout. Cancel anytime from your dashboard.
        </p>
      </header>

      {chips.length > 0 && (
        <div className="mb-5 rounded-2xl border border-stone-200 bg-stone-50 px-4 py-3 dark:border-navy-700 dark:bg-navy-900/60">
          <div className="flex flex-wrap gap-2">
            {chips.map((c) => (
              <span
                key={c.key}
                className="inline-flex max-w-full items-center rounded-full bg-white px-2.5 py-1 text-xs text-stone-700 ring-1 ring-stone-200 dark:bg-navy-800 dark:text-stone-200 dark:ring-navy-600"
              >
                <span className="mr-1 text-stone-400">{c.label}</span>
                <span className="truncate font-medium">{c.value}</span>
              </span>
            ))}
          </div>
          <button
            type="button"
            onClick={openChat}
            className="mt-2 text-xs font-medium text-navy-800 underline-offset-2 hover:underline dark:text-blue-300"
          >
            Edit in chat
          </button>
        </div>
      )}

      <div className="space-y-3">
        {PLANS.map((p) => {
          const selected = plan === p.id;
          return (
            <button
              key={p.id}
              type="button"
              disabled={submitting}
              onClick={() => void checkoutPlan(p.id)}
              className={`group flex w-full items-center gap-4 rounded-2xl border p-4 text-left transition disabled:opacity-60 ${
                selected
                  ? 'border-navy-900 bg-navy-900 text-white shadow-md dark:border-blue-500 dark:bg-blue-600'
                  : 'border-stone-200 bg-white text-gray-900 hover:border-navy-400 hover:shadow-sm dark:border-navy-700 dark:bg-navy-900 dark:text-white dark:hover:border-navy-500'
              } ${p.highlight && !selected ? 'ring-2 ring-accent-400/40' : ''}`}
            >
              <div className="min-w-0 flex-1">
                <div className="flex items-baseline justify-between gap-2">
                  <span className="text-base font-semibold">{p.name}</span>
                  <span
                    className={`shrink-0 text-sm font-semibold ${selected ? 'text-white/90' : 'text-gray-700 dark:text-stone-200'}`}
                  >
                    ${p.price}
                    <span className="font-normal opacity-70">/mo</span>
                  </span>
                </div>
                <p
                  className={`mt-0.5 text-sm ${selected ? 'text-white/80' : 'text-gray-600 dark:text-stone-400'}`}
                >
                  {p.tagline}
                </p>
              </div>
              <span
                className={`shrink-0 rounded-full px-3 py-1.5 text-xs font-semibold ${
                  selected
                    ? 'bg-white text-navy-900'
                    : 'bg-navy-900 text-white group-hover:bg-navy-800 dark:bg-blue-600'
                }`}
              >
                {submitting && plan === p.id ? '…' : 'Pay →'}
              </span>
            </button>
          );
        })}
      </div>

      {submitError && (
        <p className="mt-4 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700" role="alert">
          {submitError}
        </p>
      )}

      <p className="mt-5 text-center text-xs leading-relaxed text-gray-500 dark:text-stone-400">
        By continuing you agree to the{' '}
        <a href="/terms/" className="underline" target="_blank" rel="noreferrer">
          terms
        </a>{' '}
        and{' '}
        <a href="/privacy/" className="underline" target="_blank" rel="noreferrer">
          privacy policy
        </a>
        . Selected: <strong>{planById(plan).name}</strong> — you can switch by tapping the other
        plan.
      </p>
    </div>
  );
}
