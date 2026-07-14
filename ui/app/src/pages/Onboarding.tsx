import { useEffect, useMemo, useRef, useState, type FormEvent, type KeyboardEvent } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useAuth } from '@/providers/AuthProvider';
import { submitOnboarding, uploadCV } from '@/api/profile';
import { createCheckout } from '@/api/billing';
import { PLANS, planById, type PlanId } from '@/utils/plans';
import { useI18n } from '@/i18n/I18nProvider';
import {
  fetchMeSubscription,
  fetchOnboardingDraft,
  saveOnboardingDraft,
  sendOnboardingChat,
  type OnboardingChatFields,
  type OnboardingChatMessage,
  type OnboardingDraftFields,
} from '@/api/candidates';
import { isChatReady } from '@/onboarding/chatHeuristic';

type Phase = 'chat' | 'plan';

const WELCOME =
  "Tell me what you're looking for — paste a CV, list skills, or just describe the role and where you want to work. I'll ask only for what's still missing, then you pick a plan.";

function readPlanFromQuery(): PlanId {
  if (typeof window === 'undefined') return 'starter';
  const p = new URL(window.location.href).searchParams.get('plan');
  if (p === 'starter' || p === 'pro' || p === 'managed') return p;
  return 'starter';
}

function fieldsToDraft(f: OnboardingChatFields, plan?: PlanId): OnboardingDraftFields {
  const salary =
    f.salary_min != null || f.salary_max != null
      ? `${f.currency ?? 'USD'} ${f.salary_min ?? f.salary_max ?? ''}`
      : undefined;
  return {
    target_job_title: f.target_job_title,
    experience_level: f.experience_level as OnboardingDraftFields['experience_level'],
    job_search_status: f.job_search_status as OnboardingDraftFields['job_search_status'],
    salary_range: salary,
    wants_ats_report: true,
    preferred_regions: f.preferred_regions,
    preferred_timezones: f.preferred_timezones,
    preferred_languages: f.preferred_languages,
    job_types: f.job_types,
    country: f.country,
    plan,
  };
}

function draftToChatFields(d: OnboardingDraftFields): OnboardingChatFields {
  return {
    target_job_title: d.target_job_title,
    experience_level: d.experience_level,
    job_search_status: d.job_search_status,
    preferred_regions: d.preferred_regions,
    preferred_timezones: d.preferred_timezones,
    preferred_languages: d.preferred_languages,
    job_types: d.job_types,
    country: d.country,
  };
}

function isReady(f: OnboardingChatFields): boolean {
  return isChatReady(f);
}

const FIELD_LABELS: Record<string, string> = {
  target_job_title: 'Role',
  experience_level: 'Level',
  job_search_status: 'Search status',
  preferred_regions: 'Regions',
  country: 'Country',
  preferred_languages: 'Languages',
  job_types: 'Job types',
  salary_min: 'Salary min',
  salary_max: 'Salary max',
  currency: 'Currency',
};

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
  const [messages, setMessages] = useState<OnboardingChatMessage[]>([
    { role: 'assistant', content: WELCOME },
  ]);
  const [fields, setFields] = useState<OnboardingChatFields>({});
  const [missing, setMissing] = useState<string[]>([]);
  const [input, setInput] = useState('');
  const [sending, setSending] = useState(false);
  const [chatError, setChatError] = useState<string | null>(null);
  const [plan, setPlan] = useState<PlanId>(readPlanFromQuery);
  const [agreeTerms, setAgreeTerms] = useState(false);
  const [cv, setCv] = useState<File | undefined>();
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [draftLoaded, setDraftLoaded] = useState(false);
  const bottomRef = useRef<HTMLDivElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  // Resume draft if we already finished chat previously.
  useEffect(() => {
    if (state !== 'authenticated' || draftLoaded) return;
    let cancelled = false;
    (async () => {
      const draft = await fetchOnboardingDraft();
      if (cancelled) return;
      const f = draftToChatFields(draft.fields);
      setFields(f);
      if (draft.fields.plan) setPlan(draft.fields.plan);
      if (draft.step >= 2 || isReady(f)) {
        setPhase('plan');
        setMissing([]);
      }
      setDraftLoaded(true);
    })();
    return () => {
      cancelled = true;
    };
  }, [state, draftLoaded]);

  useEffect(() => {
    bottomRef.current?.scrollIntoView?.({ behavior: 'smooth' });
  }, [messages, sending]);

  const summaryChips = useMemo(() => {
    const chips: { key: string; label: string; value: string }[] = [];
    const f = fields;
    if (f.target_job_title)
      chips.push({ key: 'title', label: 'Role', value: f.target_job_title });
    if (f.experience_level)
      chips.push({ key: 'lvl', label: 'Level', value: f.experience_level });
    if (f.country) chips.push({ key: 'co', label: 'Country', value: f.country });
    if (f.preferred_regions?.length)
      chips.push({ key: 'reg', label: 'Regions', value: f.preferred_regions.join(', ') });
    if (f.job_types?.length)
      chips.push({ key: 'jt', label: 'Types', value: f.job_types.join(', ') });
    if (f.preferred_languages?.length)
      chips.push({ key: 'lang', label: 'Languages', value: f.preferred_languages.join(', ') });
    return chips;
  }, [fields]);

  async function sendMessage(text: string) {
    const message = text.trim();
    if (!message || sending) return;
    setSending(true);
    setChatError(null);
    const history = messages.filter((m) => m.role === 'user' || m.role === 'assistant');
    setMessages((prev) => [...prev, { role: 'user', content: message }]);
    setInput('');
    try {
      const res = await sendOnboardingChat({
        message,
        history,
        draft: fields,
      });
      // Recompute readiness client-side so defaults (region from country, etc.)
      // stay consistent even if an older server omits them.
      const ready = isChatReady(res.fields) || res.ready;
      const miss = ready ? [] : (res.missing ?? []);
      setFields(res.fields);
      setMissing(miss);
      setMessages((prev) => [...prev, { role: 'assistant', content: res.reply }]);
      const draftFields = fieldsToDraft(res.fields, plan);
      try {
        await saveOnboardingDraft(ready ? 2 : 1, draftFields);
      } catch {
        // non-blocking
      }
      if (ready) {
        setPhase('plan');
      }
    } catch (e) {
      setChatError(e instanceof Error && e.message ? e.message : t('error.somethingWrong'));
      setMessages((prev) => [
        ...prev,
        {
          role: 'assistant',
          content: "I couldn't process that just now. Try again in a moment.",
        },
      ]);
    } finally {
      setSending(false);
      textareaRef.current?.focus();
    }
  }

  function onSubmitChat(e: FormEvent) {
    e.preventDefault();
    void sendMessage(input);
  }

  function onKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      void sendMessage(input);
    }
  }

  async function finishOnboarding() {
    if (!agreeTerms) {
      setSubmitError(t('onboard.validationTerms'));
      return;
    }
    if (!isReady(fields)) {
      setPhase('chat');
      setSubmitError('A few profile details are still missing — answer the assistant first.');
      return;
    }
    setSubmitting(true);
    setSubmitError(null);
    try {
      await submitOnboarding({
        target_job_title: fields.target_job_title!,
        experience_level: fields.experience_level!,
        job_search_status: fields.job_search_status!,
        salary_min: fields.salary_min ?? undefined,
        salary_max: fields.salary_max ?? fields.salary_min ?? undefined,
        currency: fields.currency ?? 'USD',
        wants_ats_report: true,
        preferred_regions: fields.preferred_regions ?? [],
        preferred_timezones: fields.preferred_timezones ?? [],
        preferred_languages: fields.preferred_languages ?? [],
        job_types: fields.job_types ?? [],
        country: fields.country!,
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

  return (
    <div className="mx-auto flex max-w-3xl flex-col px-4 py-8 sm:px-6 lg:px-8">
      <header className="mb-6">
        <p className="text-xs font-semibold uppercase tracking-wide text-gray-500">
          {phase === 'chat' ? 'Step 1 of 2 · Tell us what you want' : 'Step 2 of 2 · Choose a plan'}
        </p>
        <h1 className="mt-1 text-3xl font-bold text-gray-900">
          {phase === 'chat' ? 'Describe your next move' : t('onboard.choosePlan')}
        </h1>
        <p className="mt-1 text-gray-600">
          {phase === 'chat'
            ? 'One chat. Paste a CV or write freely — we extract preferences with AI, then you pick a plan.'
            : t('onboard.choosePlanHint')}
        </p>
      </header>

      {/* Progress */}
      <div className="mb-6 grid grid-cols-2 gap-2" aria-hidden>
        <div className={`h-1.5 rounded-full ${phase === 'chat' || phase === 'plan' ? 'bg-accent-500' : 'bg-gray-200'}`} />
        <div className={`h-1.5 rounded-full ${phase === 'plan' ? 'bg-accent-500' : 'bg-gray-200'}`} />
      </div>

      {phase === 'chat' && (
        <>
          {summaryChips.length > 0 && (
            <div className="mb-4 flex flex-wrap gap-2" aria-label="Extracted so far">
              {summaryChips.map((c) => (
                <span
                  key={c.key}
                  className="inline-flex items-center gap-1 rounded-full border border-navy-100 bg-navy-50 px-3 py-1 text-xs font-medium text-navy-900"
                >
                  <span className="text-gray-500">{c.label}:</span> {c.value}
                </span>
              ))}
            </div>
          )}

          <div className="flex min-h-[22rem] flex-col rounded-2xl border border-gray-200 bg-white shadow-sm">
            <div className="flex-1 space-y-4 overflow-y-auto px-4 py-5 sm:px-5" role="log" aria-live="polite">
              {messages.map((m, i) => (
                <div
                  key={`${m.role}-${i}`}
                  className={`flex ${m.role === 'user' ? 'justify-end' : 'justify-start'}`}
                >
                  <div
                    className={`max-w-[85%] whitespace-pre-wrap rounded-2xl px-4 py-2.5 text-sm leading-relaxed ${
                      m.role === 'user'
                        ? 'bg-navy-900 text-white'
                        : 'bg-gray-100 text-gray-900'
                    }`}
                  >
                    {m.content}
                  </div>
                </div>
              ))}
              {sending && (
                <div className="flex justify-start">
                  <div className="rounded-2xl bg-gray-100 px-4 py-2.5 text-sm text-gray-500">
                    Thinking…
                  </div>
                </div>
              )}
              <div ref={bottomRef} />
            </div>

            <form onSubmit={onSubmitChat} className="border-t border-gray-100 p-3 sm:p-4">
              {chatError && (
                <p className="mb-2 text-xs text-red-600" role="alert">
                  {chatError}
                </p>
              )}
              <div className="flex flex-col gap-2 sm:flex-row sm:items-end">
                <textarea
                  ref={textareaRef}
                  value={input}
                  onChange={(e) => setInput(e.target.value)}
                  onKeyDown={onKeyDown}
                  rows={3}
                  placeholder="e.g. Senior product designer in Kenya, full-time, open to remote Africa… or paste your CV"
                  disabled={sending}
                  className="min-h-[5.5rem] flex-1 resize-y rounded-xl border border-gray-300 px-3 py-2.5 text-sm shadow-sm focus:border-navy-900 focus:outline-none focus:ring-1 focus:ring-navy-900 disabled:opacity-60"
                  aria-label="Describe what you want"
                />
                <button
                  type="submit"
                  disabled={sending || !input.trim()}
                  className="inline-flex h-11 shrink-0 items-center justify-center rounded-xl bg-navy-900 px-5 text-sm font-medium text-white shadow-sm hover:bg-navy-800 disabled:opacity-50"
                >
                  {sending ? 'Sending…' : 'Send'}
                </button>
              </div>
              <p className="mt-2 text-xs text-gray-500">
                Enter to send · Shift+Enter for a new line. You can paste a full CV.
              </p>
              {missing.length > 0 && (
                <p className="mt-2 text-xs text-amber-800">
                  Still need: {missing.map((m) => FIELD_LABELS[m] ?? m).join(', ')}
                </p>
              )}
              {isReady(fields) && (
                <button
                  type="button"
                  onClick={() => setPhase('plan')}
                  className="mt-3 text-sm font-medium text-navy-800 underline-offset-2 hover:underline"
                >
                  Looks good — choose a plan →
                </button>
              )}
            </form>
          </div>
        </>
      )}

      {phase === 'plan' && (
        <div className="space-y-6">
          <button
            type="button"
            onClick={() => setPhase('chat')}
            className="text-sm font-medium text-gray-600 hover:text-gray-900"
          >
            ← Back to chat
          </button>

          {summaryChips.length > 0 && (
            <div className="rounded-xl border border-gray-200 bg-gray-50 px-4 py-3">
              <p className="text-xs font-semibold uppercase tracking-wide text-gray-500">
                Your profile snapshot
              </p>
              <ul className="mt-2 space-y-1 text-sm text-gray-800">
                {summaryChips.map((c) => (
                  <li key={c.key}>
                    <span className="text-gray-500">{c.label}:</span> {c.value}
                  </li>
                ))}
              </ul>
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
                    <span className={`text-sm font-medium ${selected ? 'text-white/90' : 'text-gray-600'}`}>
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
      )}
    </div>
  );
}
