import { useEffect, useMemo, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useAuth } from '@/providers/AuthProvider';
import { fetchMeCV, submitOnboarding } from '@/api/profile';
import { createCheckout } from '@/api/billing';
import { PLANS, planById, type PlanId } from '@/utils/plans';
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

type Phase = 'chat' | 'plan';

function readPlanFromQuery(): PlanId {
  if (typeof window === 'undefined') return 'starter';
  const p = new URL(window.location.href).searchParams.get('plan');
  if (p === 'starter' || p === 'pro' || p === 'managed') return p;
  return 'starter';
}
function isChatReady(f: { extra_info?: string | null }): boolean {
  return Boolean(f.extra_info?.trim());
}
export default function Onboarding() {
  const { t } = useI18n();
  const { state, login } = useAuth();
  const wasAuthenticated = useRef(state === 'authenticated');
  const subQ = useQuery({
    queryKey: ['me-subscription'],
    queryFn: fetchMeSubscription,
    enabled: state === 'authenticated',
    staleTime: 60_000,
  });

  useEffect(() => {
    if (state !== 'authenticated') return;
    if (subQ.isLoading) return;
    if (subQ.data?.status === 'active') {
      window.location.assign('/dashboard/');
    }
  }, [state, subQ.isLoading, subQ.data?.status]);

  useEffect(() => {
    if (state === 'authenticated') {
      wasAuthenticated.current = true;
      return;
    }
    if (state === 'unauthenticated') {
      if (wasAuthenticated.current) {
        window.location.replace('/');
        return;
      }
      void login();
    }
  }, [state, login]);

  const [phase, setPhase] = useState<Phase>('chat');
  const [fields, setFields] = useState<OnboardingChatFields>({});
  const [messages, setMessages] = useState<OnboardingChatMessage[]>([]);
  const [plan, setPlan] = useState<PlanId>(readPlanFromQuery);
  const [agreeTerms, setAgreeTerms] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [draftLoaded, setDraftLoaded] = useState(false);
  /** True when a CV file (or extracted text) already exists server-side. */
  const [cvOnFile, setCvOnFile] = useState(false);
  /** Highest wizard step seen this session (never write a lower step). */
  const wizardStepRef = useRef<1 | 2 | 3>(1);
  /** Bumps when re-entering chat so PreferenceChat remounts with latest session. */
  const [chatSession, setChatSession] = useState(0);

  function bumpWizardStep(min: 1 | 2 | 3): 1 | 2 | 3 {
    const next = (wizardStepRef.current > min ? wizardStepRef.current : min) as 1 | 2 | 3;
    wizardStepRef.current = next;
    return next;
  }

  // Resume draft + full conversation (always available server-side).
  useEffect(() => {
    if (state !== 'authenticated' || draftLoaded) return;
    let cancelled = false;
    (async () => {
      const draft = await fetchOnboardingDraft();
      if (cancelled) return;
      let f = draftToChatFields(draft.fields);
      let msgs = draft.messages ?? [];
      // Hydrate capabilities from stored CV / placement when draft is thin.
      const cv = await fetchMeCV();
      if (!cancelled && cv?.present) {
        setCvOnFile(true);
        if (cv.extracted_text?.trim()) {
          const text = cv.extracted_text.trim();
          if (!f.extra_info || f.extra_info.length < text.length) {
            f = { ...f, extra_info: text };
          }
        } else if (!f.extra_info?.trim()) {
          // File exists without extractable text in GET — still count as done.
          f = {
            ...f,
            extra_info:
              'Uploaded CV on file. Resume document stored for matching (experience, education, skills).',
          };
        }
        // Skip chat phase entirely when placement is already ready.
        if (!cancelled && cv?.placement_ready && isChatReady(f)) {
          setPhase('plan');
        }
      }
      const step = (draft.step === 2 || draft.step === 3 ? draft.step : 1) as 1 | 2 | 3;
      wizardStepRef.current = step;
      // If the seeker already left chat (plan/payment step), stay there —
      // never force them back to CV upload because of a thin field snapshot.
      if (step >= 2) {
        // Ensure thread exists so "Back to chat" restores history, not landing.
        if (msgs.length === 0) {
          msgs = [
            {
              role: 'assistant',
              content:
                'Your profile is saved. Tweak anything below, or continue to plans when you are ready.',
            },
          ];
        }
        setPhase('plan');
      }
      setFields(f);
      setMessages(msgs);
      if (draft.fields.plan) setPlan(draft.fields.plan);
      setDraftLoaded(true);
    })();
    return () => {
      cancelled = true;
    };
  }, [state, draftLoaded]);

  function openChat() {
    setChatSession((n) => n + 1);
    setPhase('chat');
  }

  async function goToPlan(f: OnboardingChatFields, msgs: OnboardingChatMessage[]) {
    setFields(f);
    setMessages(msgs);
    setPhase('plan');
    const step = bumpWizardStep(2);
    // Persist step + transcript so refresh never drops subscription stage.
    try {
      await saveOnboardingDraft(step, fieldsToDraft(f, plan), msgs);
    } catch {
      // non-blocking — local phase already advanced
    }
  }

  const chips = useMemo(() => summaryChips(fields), [fields]);

  async function finishOnboarding() {
    if (!agreeTerms) {
      setSubmitError(t('onboard.validationTerms'));
      return;
    }
    setSubmitting(true);
    setSubmitError(null);
    try {
      // Mark payment stage so refresh stays on plan (not chat).
      const payStep = bumpWizardStep(3);
      const opportunityCountries = fields.preferred_countries?.length
        ? fields.preferred_countries
        : fields.country
          ? [fields.country]
          : [];
      const profilePayload = {
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
        plan,
        agree_terms: true as const,
      };

      // Profile + draft: best-effort in the background. Checkout is the
      // critical path and must not wait on onboard success or chat completeness.
      void saveOnboardingDraft(payStep, fieldsToDraft(fields, plan), messages).catch(
        () => undefined
      );
      void submitOnboarding(profilePayload).catch(() => undefined);

      // Open the payment provider (or STK pending) immediately.
      const checkout = await createCheckout({ plan_id: plan });
      if (checkout.status === 'redirect' && checkout.redirect_url) {
        window.location.assign(checkout.redirect_url);
        return;
      }
      if (checkout.status === 'pending' && checkout.prompt_id) {
        window.location.assign(
          `/dashboard/?billing=pending&prompt_id=${encodeURIComponent(checkout.prompt_id)}`
        );
        return;
      }
      if (checkout.status === 'paid') {
        window.location.assign('/dashboard/?billing=success');
        return;
      }
      setSubmitError(checkout.error || 'Could not start checkout. Please try again.');
    } catch (e) {
      setSubmitError(e instanceof Error && e.message ? e.message : t('error.somethingWrong'));
    } finally {
      setSubmitting(false);
    }
  }

  if (state === 'unauthenticated' || state === 'initializing') {
    return (
      <div className="mx-auto flex min-h-[40vh] max-w-md items-center justify-center px-4 py-16 text-center">
        <p className="text-sm text-gray-600">{t('onboard.openingSignIn')}</p>
      </div>
    );
  }

  if (phase === 'chat') {
    // Chat shell above the global site footer (do not use 100dvh lock that
    // pushes chrome off-screen).
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
            completeLabel="Continue to plans"
            className="flex min-h-0 flex-1 flex-col"
            onFieldsChange={(f, meta) => {
              setFields(f);
              setMessages(meta.messages);
              // Keep draft warm; never write a step lower than wizardStep
              // (user may already be on plan and only tweaking chat).
              const step = bumpWizardStep(meta.ready ? 2 : 1);
              void saveOnboardingDraft(step, fieldsToDraft(f, plan), meta.messages).catch(
                () => undefined
              );
            }}
            onComplete={(f, meta) => goToPlan(f, meta?.messages ?? messages)}
          />
        ) : (
          <div className="flex flex-1 items-center justify-center text-sm text-stone-400">
            Loading…
          </div>
        )}
      </div>
    );
  }

  return (
    <div className="mx-auto flex max-w-3xl flex-col px-4 py-8 sm:px-6 lg:px-8">
      <header className="mb-6">
        <p className="text-xs font-semibold uppercase tracking-wide text-gray-500">
          Step 2 of 2 · Choose a plan
        </p>
        <h1 className="mt-1 text-3xl font-bold text-gray-900">{t('onboard.choosePlan')}</h1>
        <p className="mt-1 text-gray-600">{t('onboard.choosePlanHint')}</p>
      </header>

      <div className="space-y-6">
        <button
          type="button"
          onClick={openChat}
          className="text-sm font-medium text-gray-600 hover:text-gray-900"
        >
          ← Back to chat
        </button>

        {chips.length > 0 && (
          <div className="rounded-xl border border-gray-200 bg-gray-50 px-4 py-3">
            <p className="text-xs font-semibold uppercase tracking-wide text-gray-500">
              Your profile snapshot
            </p>
            <ul className="mt-2 space-y-1 text-sm text-gray-800">
              {chips.map((c) => (
                <li key={c.key}>
                  <span className="text-gray-500">{c.label}:</span> {c.value}
                </li>
              ))}
            </ul>
            <button
              type="button"
              onClick={openChat}
              className="mt-2 text-xs font-medium text-navy-800 underline-offset-2 hover:underline"
            >
              Tweak in chat
            </button>
          </div>
        )}

        <div className="grid gap-4 sm:grid-cols-3">
          {PLANS.map((p) => {
            const selected = plan === p.id;
            return (
              <button
                key={p.id}
                type="button"
                onClick={() => setPlan(p.id)}
                className={`rounded-2xl border p-4 text-left transition-shadow ${
                  selected
                    ? 'border-navy-900 bg-navy-900 text-white shadow-md'
                    : 'border-gray-200 bg-white text-gray-900 hover:border-gray-300'
                } ${p.highlight && !selected ? 'ring-2 ring-accent-400/40' : ''}`}
              >
                <div className="flex items-baseline justify-between gap-2">
                  <span className="text-lg font-semibold">{p.name}</span>
                  <span
                    className={`text-sm font-medium ${selected ? 'text-white/90' : 'text-gray-600'}`}
                  >
                    ${p.price}/mo
                  </span>
                </div>
                <p className={`mt-1 text-sm ${selected ? 'text-white/80' : 'text-gray-600'}`}>
                  {p.tagline}
                </p>
              </button>
            );
          })}
        </div>

        <label className="flex items-start gap-2 text-sm text-gray-700">
          <input
            type="checkbox"
            checked={agreeTerms}
            onChange={(e) => setAgreeTerms(e.target.checked)}
            className="mt-1 rounded border-gray-300 text-navy-900 focus:ring-navy-900"
          />
          <span>
            I agree to the{' '}
            <a href="/terms/" className="underline" target="_blank" rel="noreferrer">
              terms of service
            </a>{' '}
            and{' '}
            <a href="/privacy/" className="underline" target="_blank" rel="noreferrer">
              privacy policy
            </a>
            .
          </span>
        </label>

        {submitError && (
          <p className="rounded bg-red-50 px-3 py-2 text-sm text-red-700" role="alert">
            {submitError}
          </p>
        )}

        <button
          type="button"
          disabled={submitting || !agreeTerms}
          onClick={() => void finishOnboarding()}
          className="w-full rounded-xl bg-navy-900 px-6 py-3 text-sm font-medium text-white shadow-sm hover:bg-navy-800 disabled:opacity-50 sm:w-auto"
        >
          {submitting
            ? t('onboard.submitting')
            : `${t('onboard.continueToPayment')} · $${planById(plan).price}${t('dash.perMonth')}`}
        </button>
      </div>
    </div>
  );
}
