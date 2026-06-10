import { useEffect, useId, useMemo, useState } from "react";
import { useForm, type SubmitHandler, type UseFormReturn } from "react-hook-form";
import { z } from "zod";
import { useQuery } from "@tanstack/react-query";
import { useAuth } from "@/providers/AuthProvider";
import { submitOnboarding, uploadCV } from "@/api/profile";
import { createCheckout } from "@/api/billing";
import { PLANS, planById, type PlanId } from "@/utils/plans";
import { useI18n } from "@/i18n/I18nProvider";
import type { StringKey } from "@/i18n/strings";
import {
  fetchMeSubscription,
  fetchOnboardingDraft,
  saveOnboardingDraft,
  type OnboardingDraftFields,
} from "@/api/candidates";

type FormValues = Omit<z.infer<typeof Step1Schema>, 'cv'> & { cv?: File } & z.infer<
    typeof Step2Schema
  > &
  z.infer<typeof Step3Schema>;

const Step1Schema = z
  .object({
    cv: z
      .any()
      .optional()
      .refine((v) => !v || v instanceof File, 'Invalid file')
      .refine(
        (v) => !(v instanceof File) || v.size <= 10 * 1024 * 1024,
        'CV must be 10 MB or smaller'
      )
      .refine(
        (v) => !(v instanceof File) || /\.(pdf|docx?|rtf|txt)$/i.test(v.name),
        'Upload a PDF, DOCX, RTF, or TXT file'
      ),
    extraInfo: z.string().optional(),
    salaryAmount: z.coerce.number().nonnegative().optional(),
    salaryCurrency: z.string().optional(),
  })
  .refine((d) => d.cv instanceof File || (d.extraInfo && d.extraInfo.trim().length > 0), {
    path: ['extraInfo'],
    message: 'Provide a CV or tell us about yourself',
  });

const Step2Schema = z.object({
  preferredRegions: z.array(z.string()).min(1),
  country: z.string().min(2),
  preferredTimezones: z.array(z.string()),
  preferredLanguages: z.array(z.string()).min(1),
  jobTypes: z.array(z.string()).min(1),
});

const Step3Schema = z.object({
  plan: z.enum(['starter', 'pro', 'managed']),
  agreeTerms: z.literal(true),
});

const STEP_LABEL_KEYS: StringKey[] = [
  'onboard.aboutYou',
  'onboard.yourPreferences',
  'onboard.choosePlan',
];

const REGION_KEYS: { value: string; labelKey: StringKey }[] = [
  { value: 'Anywhere', labelKey: 'onboard.anywhere' },
  { value: 'Africa', labelKey: 'onboard.africa' },
  { value: 'Europe', labelKey: 'onboard.europe' },
  { value: 'North America', labelKey: 'onboard.northAmerica' },
  { value: 'South America', labelKey: 'onboard.southAmerica' },
  { value: 'Asia', labelKey: 'onboard.asia' },
  { value: 'Oceania', labelKey: 'onboard.oceania' },
];

const TIMEZONES = [
  'EAT (UTC+3)',
  'WAT (UTC+1)',
  'CAT (UTC+2)',
  'SAST (UTC+2)',
  'GMT (UTC+0)',
  'CET (UTC+1)',
  'EST (UTC-5)',
  'PST (UTC-8)',
];

const LANGUAGE_KEYS: { value: string; labelKey: StringKey }[] = [
  { value: 'English', labelKey: 'onboard.anywhere' },
  { value: 'French', labelKey: 'onboard.anywhere' },
  { value: 'Arabic', labelKey: 'onboard.anywhere' },
  { value: 'Swahili', labelKey: 'onboard.anywhere' },
  { value: 'Portuguese', labelKey: 'onboard.anywhere' },
  { value: 'Spanish', labelKey: 'onboard.anywhere' },
  { value: 'German', labelKey: 'onboard.anywhere' },
  { value: 'Mandarin', labelKey: 'onboard.anywhere' },
];

const JOB_TYPE_KEYS: { value: string; labelKey: StringKey }[] = [
  { value: 'Full-time', labelKey: 'onboard.fullTime' },
  { value: 'Part-time', labelKey: 'onboard.partTime' },
  { value: 'Contract', labelKey: 'onboard.contract' },
  { value: 'Freelance', labelKey: 'onboard.freelance' },
  { value: 'Internship', labelKey: 'onboard.internship' },
];

const CURRENCIES = [
  'USD',
  'EUR',
  'GBP',
  'KES',
  'NGN',
  'ZAR',
  'GHS',
  'AED',
  'INR',
  'JPY',
  'CNY',
  'BRL',
  'MXN',
  'CAD',
  'AUD',
  'CHF',
  'SGD',
  'SAR',
];

function readPlanFromQuery(): PlanId {
  if (typeof window === 'undefined') return 'starter';
  const p = new URL(window.location.href).searchParams.get('plan');
  if (p === 'starter' || p === 'pro' || p === 'managed') return p;
  return 'starter';
}

export default function Onboarding() {
  const { t } = useI18n();
  const { state, login } = useAuth();
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

  const [step, setStep] = useState<1 | 2 | 3>(1);
  const [draftLoaded, setDraftLoaded] = useState(false);
  const [draftSaveWarning, setDraftSaveWarning] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const initialPlan = useMemo(readPlanFromQuery, []);
  const form = useForm<FormValues>({
    defaultValues: {
      cv: undefined,
      extraInfo: '',
      salaryAmount: undefined,
      salaryCurrency: 'USD',
      preferredRegions: [],
      preferredTimezones: [],
      preferredLanguages: ['English'],
      jobTypes: ['Full-time'],
      country: '',
      plan: initialPlan,
      agreeTerms: false as unknown as true,
    },
    mode: 'onBlur',
  });

  useEffect(() => {
    if (state === 'unauthenticated') {
      void login();
    }
  }, [state, login]);

  useEffect(() => {
    if (state !== 'authenticated') return;
    if (draftLoaded) return;
    let cancelled = false;
    (async () => {
      const draft = await fetchOnboardingDraft();
      if (cancelled) return;
      form.reset(
        { ...form.getValues(), ...(draft.fields as Record<string, unknown>) },
        { keepDirty: false, keepDefaultValues: true }
      );
      setStep(draft.step);
      setDraftLoaded(true);
    })();
    return () => {
      cancelled = true;
    };
  }, [state, draftLoaded, form]);

  if (state === 'unauthenticated' || state === 'initializing') {
    return (
      <div className="mx-auto flex min-h-[40vh] max-w-md items-center justify-center px-4 py-16 text-center">
        <p className="text-sm text-gray-600">{t('onboard.openingSignIn')}</p>
      </div>
    );
  }

  const onSubmit: SubmitHandler<FormValues> = async (data) => {
    setSubmitting(true);
    setSubmitError(null);
    try {
      await submitOnboarding({
        target_job_title: '',
        experience_level: 'mid',
        job_search_status: 'actively_looking',
        salary_min: data.salaryAmount ?? undefined,
        salary_max: data.salaryAmount ?? undefined,
        currency: data.salaryCurrency ?? 'USD',
        wants_ats_report: true,
        preferred_regions: data.preferredRegions,
        preferred_timezones: data.preferredTimezones,
        preferred_languages: data.preferredLanguages,
        job_types: data.jobTypes,
        country: data.country,
        plan: data.plan,
        agree_terms: data.agreeTerms,
      });

      if (data.cv instanceof File) {
        try {
          await uploadCV(data.cv);
        } catch (cvErr) {
          console.warn('[onboarding] CV upload failed (profile saved):', cvErr);
        }
      }

      try {
        const checkout = await createCheckout({ plan_id: data.plan });
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
  };

  function validateStep(s: 1 | 2 | 3): boolean {
    const values = form.getValues();
    const schemas: Record<number, z.ZodTypeAny> = {
      1: Step1Schema,
      2: Step2Schema,
      3: Step3Schema,
    };
    const parsed = schemas[s]!.safeParse(values);
    if (parsed.success) return true;

    const msgMap: Record<string, StringKey> = {
      extraInfo: 'onboard.validationCVOrInfo',
      cv: 'onboard.validationCV',
      preferredRegions: 'onboard.validationRegion',
      country: 'onboard.validationCountry',
      preferredLanguages: 'onboard.validationLanguage',
      jobTypes: 'onboard.validationJobType',
      agreeTerms: 'onboard.validationTerms',
    };
    for (const issue of parsed.error.issues) {
      const field = issue.path[0] as keyof FormValues;
      const key = msgMap[field as string];
      form.setError(field, { message: key ? t(key) : issue.message });
    }
    return false;
  }

  async function next() {
    if (!validateStep(step)) return;
    if (step < 3) {
      const nextStep = (step + 1) as 1 | 2 | 3;
      const values = form.getValues();
      const fieldsForServer: OnboardingDraftFields = {
        target_job_title: '',
        experience_level: 'mid',
        job_search_status: 'actively_looking',
        salary_range: values.salaryAmount
          ? `${values.salaryCurrency ?? 'USD'} ${values.salaryAmount}`
          : undefined,
        wants_ats_report: true,
        preferred_regions: values.preferredRegions,
        preferred_timezones: values.preferredTimezones,
        preferred_languages: values.preferredLanguages,
        job_types: values.jobTypes,
        country: values.country,
        plan: values.plan,
      };
      try {
        await saveOnboardingDraft(nextStep, fieldsForServer);
        setDraftSaveWarning(false);
      } catch {
        setDraftSaveWarning(true);
      }
      setStep(nextStep);
    } else await form.handleSubmit(onSubmit)();
  }

  const selectedPlan = form.watch('plan');
  const finishLabel =
    step === 3
      ? `${t('onboard.continueToPayment')} · $${planById(selectedPlan).price}${t('dash.perMonth')}`
      : t('onboard.continue');

  return (
    <div className="mx-auto max-w-4xl px-4 py-8 sm:px-6 lg:px-8">
      {draftSaveWarning && (
        <div
          role="status"
          className="mb-4 rounded-md border border-amber-300 bg-amber-50 px-4 py-3 text-sm text-amber-800"
        >
          {t('onboard.draftSaveWarning')}
        </div>
      )}
      <Progress step={step} t={t} />
      <form
        className="mt-8"
        onSubmit={(e) => {
          e.preventDefault();
          void next();
        }}
      >
        {step === 1 && <Step1Form form={form} t={t} />}
        {step === 2 && <Step2Form form={form} t={t} />}
        {step === 3 && <Step3Form form={form} t={t} />}
        {submitError && (
          <p className="mt-4 rounded bg-red-50 px-3 py-2 text-sm text-red-700" role="alert">
            {submitError}
          </p>
        )}
        <div className="mt-8 flex items-center justify-between">
          <button
            type="button"
            disabled={step === 1 || submitting}
            onClick={() => setStep((s) => Math.max(1, s - 1) as 1 | 2 | 3)}
            className="rounded border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 disabled:opacity-40"
          >
            {t('onboard.back')}
          </button>
          <button
            type="submit"
            disabled={submitting}
            className="rounded bg-navy-900 px-6 py-2.5 text-sm font-medium text-white shadow-sm hover:bg-navy-800 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-navy-900 disabled:opacity-60"
          >
            {submitting ? t('onboard.submitting') : finishLabel}
          </button>
        </div>
      </form>
    </div>
  );
}

type T = (k: StringKey, fallback?: string) => string;

function Progress({ step, t }: { step: 1 | 2 | 3; t: T }) {
  return (
    <div
      role="progressbar"
      aria-label="Onboarding progress"
      aria-valuemin={1}
      aria-valuemax={3}
      aria-valuenow={step}
    >
      <p className="text-xs font-semibold uppercase tracking-wide text-gray-500">
        {t('onboard.step')} {step} {t('onboard.of')} 3 · {t(STEP_LABEL_KEYS[step - 1]!)}
      </p>
      <ol className="mt-3 grid grid-cols-3 gap-2" aria-hidden>
        {STEP_LABEL_KEYS.map((key, i) => {
          const n = (i + 1) as 1 | 2 | 3;
          const done = step > n;
          const active = step === n;
          return (
            <li key={key} className="flex flex-col gap-1">
              <div
                className={`h-1.5 rounded-full transition-colors ${
                  done || active ? 'bg-accent-500' : 'bg-gray-200'
                }`}
              />
              <span className={`text-xs ${active ? 'font-medium text-gray-900' : 'text-gray-500'}`}>
                {t(key)}
              </span>
            </li>
          );
        })}
      </ol>
    </div>
  );
}

type FormProps = { form: UseFormReturn<FormValues>; t: T };

function Step1Form({ form, t }: FormProps) {
  const {
    register,
    watch,
    setValue,
    formState: { errors },
  } = form;
  const cv = watch('cv') as File | undefined;
  return (
    <div className="space-y-6">
      <header>
        <h1 className="text-3xl font-bold text-gray-900">{t('onboard.aboutYou')}</h1>
        <p className="mt-1 text-gray-600">{t('onboard.aboutYouHint')}</p>
      </header>

      <Field label={t('onboard.uploadCV')} error={errors.cv?.message as string | undefined}>
        {(id) => (
          <div className="rounded-lg border-2 border-dashed border-gray-300 bg-gray-50 p-4">
            {cv ? (
              <div className="flex items-center justify-between gap-3">
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium text-gray-900">{cv.name}</p>
                  <p className="text-xs text-gray-500">
                    {(cv.size / 1024).toFixed(1)} KB · {t('onboard.readyToUpload')}
                  </p>
                </div>
                <button
                  type="button"
                  onClick={() => setValue('cv', undefined, { shouldValidate: true })}
                  className="text-sm font-medium text-gray-600 hover:text-gray-900"
                >
                  {t('onboard.remove')}
                </button>
              </div>
            ) : (
              <div className="text-center">
                <input
                  id={id}
                  type="file"
                  accept=".pdf,.doc,.docx,.rtf,.txt"
                  onChange={(e) => {
                    const f = e.target.files?.[0];
                    setValue('cv', f ?? undefined, { shouldValidate: true });
                  }}
                  className="sr-only"
                />
                <label
                  htmlFor={id}
                  className="inline-flex cursor-pointer items-center rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-700 shadow-sm hover:bg-gray-50"
                >
                  <svg
                    className="mr-2 h-4 w-4"
                    fill="none"
                    stroke="currentColor"
                    viewBox="0 0 24 24"
                    aria-hidden="true"
                  >
                    <path
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      strokeWidth="2"
                      d="M7 16a4 4 0 01-.88-7.903A5 5 0 0115.9 6L16 6a5 5 0 011 9.9M15 13l-3-3m0 0l-3 3m3-3v12"
                    />
                  </svg>
                  {t('onboard.chooseFile')}
                </label>
                <p className="mt-2 text-xs text-gray-500">{t('onboard.cvFormats')}</p>
              </div>
            )}
          </div>
        )}
      </Field>
      <p className="text-xs text-gray-500">{t('onboard.cvPrivacy')}</p>

      <Field label={t('onboard.extraInfo')} error={errors.extraInfo?.message as string | undefined}>
        {(id) => (
          <textarea
            id={id}
            rows={3}
            placeholder={t('onboard.extraInfoPlaceholder')}
            {...register('extraInfo')}
            className="w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-navy-900 focus:outline-none focus:ring-1 focus:ring-navy-900"
          />
        )}
      </Field>

      <Field label={t('onboard.targetSalary')}>
        {() => (
          <div className="flex gap-3">
            <select
              {...register('salaryCurrency')}
              className="w-28 rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-navy-900 focus:outline-none focus:ring-1 focus:ring-navy-900"
            >
              {CURRENCIES.map((c) => (
                <option key={c} value={c}>
                  {c}
                </option>
              ))}
            </select>
            <input
              type="number"
              min="0"
              step="1000"
              placeholder={t('onboard.salaryPlaceholder')}
              {...register('salaryAmount', { valueAsNumber: true })}
              className="flex-1 rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-navy-900 focus:outline-none focus:ring-1 focus:ring-navy-900"
            />
          </div>
        )}
      </Field>
    </div>
  );
}

function Step2Form({ form, t }: FormProps) {
  const {
    watch,
    setValue,
    register,
    formState: { errors },
  } = form;
  const selectedRegions = watch('preferredRegions');
  const selectedTZ = watch('preferredTimezones');
  const selectedLangs = watch('preferredLanguages');
  const selectedJobTypes = watch('jobTypes');
  const anywhereSelected = selectedRegions.includes('Anywhere');

  function toggleRegion(r: string) {
    if (r === 'Anywhere') {
      setValue('preferredRegions', anywhereSelected ? [] : ['Anywhere'], { shouldValidate: true });
      return;
    }
    const withoutAnywhere = selectedRegions.filter((x) => x !== 'Anywhere');
    const on = withoutAnywhere.includes(r);
    setValue(
      'preferredRegions',
      on ? withoutAnywhere.filter((x) => x !== r) : [...withoutAnywhere, r],
      { shouldValidate: true }
    );
  }

  return (
    <div className="space-y-6">
      <header>
        <h1 className="text-3xl font-bold text-gray-900">{t('onboard.yourPreferences')}</h1>
        <p className="mt-1 text-gray-600">{t('onboard.preferencesHint')}</p>
      </header>
      <Field
        label={t('onboard.regions')}
        error={errors.preferredRegions?.message as string | undefined}
      >
        {() => (
          <div className="flex flex-wrap gap-2" role="group" aria-label={t('onboard.regions')}>
            {REGION_KEYS.map(({ value, labelKey }) => {
              const on = selectedRegions.includes(value);
              return (
                <button
                  key={value}
                  type="button"
                  aria-pressed={on}
                  className={`min-h-[44px] rounded-full border px-4 py-2 text-sm font-medium transition-colors ${
                    on
                      ? 'border-navy-900 bg-navy-900 text-white'
                      : 'border-gray-200 bg-white text-gray-700 hover:border-gray-300 hover:bg-gray-50'
                  }`}
                  onClick={() => toggleRegion(value)}
                >
                  {t(labelKey)}
                </button>
              );
            })}
          </div>
        )}
      </Field>
      <Field label={t('onboard.timezones')}>
        {() => (
          <div className="flex flex-wrap gap-2" role="group" aria-label={t('onboard.timezones')}>
            {TIMEZONES.map((tz) => {
              const on = selectedTZ.includes(tz);
              return (
                <button
                  key={tz}
                  type="button"
                  aria-pressed={on}
                  className={`min-h-[44px] rounded-full border px-4 py-2 text-sm font-medium transition-colors ${
                    on
                      ? 'border-navy-900 bg-navy-900 text-white'
                      : 'border-gray-200 bg-white text-gray-700 hover:border-gray-300 hover:bg-gray-50'
                  }`}
                  onClick={() =>
                    setValue(
                      'preferredTimezones',
                      on ? selectedTZ.filter((x) => x !== tz) : [...selectedTZ, tz]
                    )
                  }
                >
                  {tz}
                </button>
              );
            })}
          </div>
        )}
      </Field>
      <Field
        label={t('onboard.languages')}
        error={errors.preferredLanguages?.message as string | undefined}
      >
        {() => (
          <div className="flex flex-wrap gap-2" role="group" aria-label={t('onboard.languages')}>
            {LANGUAGE_KEYS.map(({ value }) => {
              const on = selectedLangs.includes(value);
              return (
                <button
                  key={value}
                  type="button"
                  aria-pressed={on}
                  className={`min-h-[44px] rounded-full border px-4 py-2 text-sm font-medium transition-colors ${
                    on
                      ? 'border-navy-900 bg-navy-900 text-white'
                      : 'border-gray-200 bg-white text-gray-700 hover:border-gray-300 hover:bg-gray-50'
                  }`}
                  onClick={() => {
                    setValue(
                      'preferredLanguages',
                      on ? selectedLangs.filter((x) => x !== value) : [...selectedLangs, value],
                      { shouldValidate: true }
                    );
                  }}
                >
                  {value}
                </button>
              );
            })}
          </div>
        )}
      </Field>
      <Field label={t('onboard.jobType')} error={errors.jobTypes?.message as string | undefined}>
        {() => (
          <div className="flex flex-wrap gap-2" role="group" aria-label={t('onboard.jobType')}>
            {JOB_TYPE_KEYS.map(({ value, labelKey }) => {
              const on = selectedJobTypes.includes(value);
              return (
                <button
                  key={value}
                  type="button"
                  aria-pressed={on}
                  className={`min-h-[44px] rounded-full border px-4 py-2 text-sm font-medium transition-colors ${
                    on
                      ? 'border-navy-900 bg-navy-900 text-white'
                      : 'border-gray-200 bg-white text-gray-700 hover:border-gray-300 hover:bg-gray-50'
                  }`}
                  onClick={() => {
                    setValue(
                      'jobTypes',
                      on
                        ? selectedJobTypes.filter((x) => x !== value)
                        : [...selectedJobTypes, value],
                      { shouldValidate: true }
                    );
                  }}
                >
                  {t(labelKey)}
                </button>
              );
            })}
          </div>
        )}
      </Field>
      <Field label={t('onboard.country')} error={errors.country?.message}>
        {(id) => (
          <input
            id={id}
            type="text"
            autoComplete="country-name"
            placeholder={t('onboard.countryPlaceholder')}
            {...register('country')}
            className="w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-navy-900 focus:outline-none focus:ring-1 focus:ring-navy-900"
          />
        )}
      </Field>
    </div>
  );
}

function Step3Form({ form, t }: FormProps) {
  const {
    register,
    watch,
    setValue,
    formState: { errors },
  } = form;
  const plan = watch('plan');

  return (
    <div className="space-y-6">
      <header>
        <h1 className="text-3xl font-bold text-gray-900">{t('onboard.choosePlan')}</h1>
        <p className="mt-1 text-gray-600">{t('onboard.choosePlanHint')}</p>
      </header>

      <Field error={errors.plan?.message as string | undefined}>
        {() => (
          <div className="grid gap-3 sm:grid-cols-3" role="radiogroup" aria-label="Plan">
            {PLANS.map((p) => {
              const on = plan === p.id;
              const priceLabel = `$${p.price}${t('dash.perMonth')}`;
              return (
                <button
                  key={p.id}
                  type="button"
                  role="radio"
                  aria-checked={on}
                  onClick={() => setValue('plan', p.id, { shouldValidate: true })}
                  className={`flex flex-col items-start gap-1 rounded-lg border-2 p-4 text-left transition-colors ${
                    on
                      ? 'border-accent-500 bg-accent-50'
                      : 'border-gray-200 bg-white hover:border-gray-300'
                  }`}
                >
                  <div className="flex w-full items-center justify-between gap-2">
                    <span className="text-base font-semibold text-gray-900">{p.name}</span>
                    <span className="text-sm font-semibold text-gray-900">{priceLabel}</span>
                  </div>
                  <p className="text-sm text-gray-600">{p.tagline}</p>
                  {p.matchesPerWeek !== null && (
                    <p className="mt-1 text-xs text-gray-500">
                      {t('onboard.matchesPerWeek').replace('{count}', String(p.matchesPerWeek))}
                    </p>
                  )}
                  {p.meta.agent && (
                    <p className="mt-1 text-xs font-medium text-accent-700">
                      {t('onboard.includesAgent')}
                    </p>
                  )}
                </button>
              );
            })}
          </div>
        )}
      </Field>

      <Field error={errors.agreeTerms?.message as string | undefined}>
        {() => (
          <label className="flex items-start gap-3">
            <input
              type="checkbox"
              {...register('agreeTerms')}
              className="mt-0.5 h-4 w-4 rounded border-gray-300 text-navy-900 focus:ring-navy-900"
            />
            <span className="text-sm text-gray-700">
              {t('onboard.agreeTermsLabel')}{' '}
              <a
                href="/terms/"
                className="font-medium text-accent-600 underline hover:text-accent-700"
              >
                {t('footer.terms')}
              </a>{' '}
              {t('onboard.agreeTermsAnd')}{' '}
              <a
                href="/privacy/"
                className="font-medium text-accent-600 underline hover:text-accent-700"
              >
                {t('footer.privacyPolicy')}
              </a>
              .
            </span>
          </label>
        )}
      </Field>

      <p className="text-xs text-gray-500">{t('onboard.paymentRedirectHint')}</p>
    </div>
  );
}

function Field({
  label,
  error,
  children,
}: {
  label?: string;
  error?: string;
  children: (id: string) => React.ReactNode;
}) {
  const id = useId();
  return (
    <div>
      {label && (
        <label htmlFor={id} className="block text-sm font-medium text-gray-700">
          {label}
        </label>
      )}
      <div className={label ? 'mt-1' : ''}>{children(id)}</div>
      {error && (
        <p className="mt-1 text-sm text-red-600" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}