import { useEffect, useMemo, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useAuth } from '@/providers/AuthProvider';
import { fetchMeCV, submitOnboarding, uploadCV } from '@/api/profile';
import { createCheckout } from '@/api/billing';
import { PLANS, planById, type PlanId } from '@/utils/plans';
import { useI18n } from '@/i18n/I18nProvider';
import {
  fetchMeSubscription,
  fetchOnboardingDraft,
  type OnboardingChatFields,
  type OnboardingChatMessage,
} from '@/api/candidates';
import { isChatReady } from '@/onboarding/chatHeuristic';
import { PreferenceChat, draftToChatFields, summaryChips } from '@/components/preference-chat';

type Phase = 'chat' | 'plan';

function readPlanFromQuery(): PlanId {
  if (typeof window === 'undefined') return 'starter';
  const p = new URL(window.location.href).searchParams.get('plan');
  if (p === 'starter' || p === 'pro' || p === 'managed') return p;
  return 'starter';
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
  const [cv, setCv] = useState<File | undefined>();
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [draftLoaded, setDraftLoaded] = useState(false);
  /** Bumps when re-entering chat so PreferenceChat remounts with latest session. */
  const [chatSession, setChatSession] = useState(0);

  // Resume draft + full conversation (always available server-side).
  useEffect(() => {
    if (state !== 'authenticated' || draftLoaded) return;
    let cancelled = false;
    (async () => {
      const draft = await fetchOnboardingDraft();
      if (cancelled) return;
      let f = draftToChatFields(draft.fields);
      // Hydrate capabilities from the local CV document index when chat draft
      // lacks CV text (e.g. uploaded earlier via settings / side chat).
      if (!f.extra_info || f.extra_info.length < 80) {
        const cv = await fetchMeCV();
        if (!cancelled && cv?.present && cv.extracted_text?.trim()) {
          f = { ...f, extra_info: cv.extracted_text.trim() };
        }
      }
      setFields(f);
      setMessages(draft.messages ?? []);
      if (draft.fields.plan) setPlan(draft.fields.plan);
      // Resume to plan only when ready AND the user already left chat (step ≥ 2).
      if (draft.step >= 2 && isChatReady(f)) {
        setPhase('plan');
      }
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

  const chips = useMemo(() => summaryChips(fields), [fields]);

  async function finishOnboarding() {
    if (!agreeTerms) {
      setSubmitError(t('onboard.validationTerms'));
      return;
    }
    if (!isChatReady(fields)) {
      setPhase('chat');
      setSubmitError('A few profile details are still missing — answer the assistant first.');
      return;
    }
    setSubmitting(true);
    setSubmitError(null);
    try {
      const opportunityCountries = fields.preferred_countries?.length
        ? fields.preferred_countries
        : fields.country
          ? [fields.country]
          : [];
      await submitOnboarding({
        target_job_title: fields.target_job_title!,
        experience_level: fields.experience_level!,
        job_search_status: fields.job_search_status ?? 'actively_looking',
        salary_min: fields.salary_min ?? undefined,
        salary_max: fields.salary_max ?? fields.salary_min ?? undefined,
        currency: fields.currency ?? 'USD',
        wants_ats_report: true,
        preferred_regions: fields.preferred_regions ?? [],
        preferred_timezones: fields.preferred_timezones ?? [],
        preferred_languages: fields.preferred_languages ?? [],
        job_types: fields.job_types ?? [],
        // Primary country for profile; full list is also in regions/job prefs.
        country: opportunityCountries[0] ?? fields.country!,
        plan,
        agree_terms: true,
      });

      if (cv instanceof File) {
        try {
          await uploadCV(cv);
        } catch {
          setSubmitError(
            'Your profile was saved, but the CV file failed to upload. You can re-upload it later from Settings.'
          );
        }
      }

      try {
        const checkout = await createCheckout({ plan_id: plan });
        if (checkout.status === 'redirect' && checkout.redirect_url) {
          window.location.href = checkout.redirect_url;
          return;
        }
        if (checkout.status === 'pending' && checkout.prompt_id) {
          window.location.href = `/dashboard/?billing=pending&prompt_id=${encodeURIComponent(checkout.prompt_id)}`;
          return;
        }
        if (checkout.status === 'paid') {
          window.location.href = '/dashboard/?billing=success';
          return;
        }
        throw new Error(checkout.error || 'Checkout did not complete.');
      } catch {
        window.location.href = '/dashboard/?billing=failed';
        return;
      }
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
            showCompleteAction
            completeLabel="Continue to plans"
            className="flex min-h-0 flex-1 flex-col"
            onFieldsChange={(f, meta) => {
              setFields(f);
              setMessages(meta.messages);
            }}
            onComplete={(f) => {
              setFields(f);
              setPhase('plan');
            }}
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

        <div className="rounded-lg border border-dashed border-gray-300 bg-gray-50 p-4">
          <label className="block text-sm font-medium text-gray-900">
            Optional: upload CV file
          </label>
          <p className="mt-0.5 text-xs text-gray-500">
            If you only pasted text above, a PDF/DOCX helps matching quality.
          </p>
          <input
            type="file"
            accept=".pdf,.doc,.docx,.rtf,.txt"
            className="mt-2 block w-full text-sm text-gray-600 file:mr-3 file:rounded-md file:border-0 file:bg-navy-900 file:px-3 file:py-1.5 file:text-sm file:font-medium file:text-white"
            onChange={(e) => setCv(e.target.files?.[0] ?? undefined)}
          />
          {cv && (
            <p className="mt-1 truncate text-xs text-gray-600">
              {cv.name} ({(cv.size / 1024).toFixed(0)} KB)
            </p>
          )}
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
