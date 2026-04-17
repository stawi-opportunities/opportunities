import { useId, useMemo, useState } from "react";
import { useForm, type SubmitHandler, type UseFormReturn } from "react-hook-form";
import { z } from "zod";
import { useAuth } from "@/providers/AuthProvider";
import { submitOnboarding } from "@/api/candidates";
import { PLANS, planById, type PlanId } from "@/utils/plans";

// Each step owns a contiguous slice of fields; we validate one step at a
// time before advancing so errors surface close to the inputs the user
// just touched.
//
// Step 3 is the plan picker + terms. The user arrives at /onboarding/
// with an optional ?plan=<id> query param from the home / pricing CTAs —
// we preselect that tier but they can change before finishing.

const Step1 = z.object({
  targetJobTitle: z.string().min(2, "Enter a target job title"),
  experienceLevel: z.enum(["entry", "junior", "mid", "senior", "lead", "executive"]),
  jobSearchStatus: z.enum(["actively_looking", "open_to_offers", "casually_browsing"]),
  salaryRange: z.string().optional(),
  wantsATSReport: z.boolean(),
  cv: z
    .any()
    .refine((v) => v instanceof File, "Upload your CV to continue")
    .refine(
      (v) => !(v instanceof File) || v.size <= 10 * 1024 * 1024,
      "CV must be 10 MB or smaller",
    )
    .refine(
      (v) => !(v instanceof File) || /\.(pdf|docx?|rtf|txt)$/i.test(v.name),
      "Upload a PDF, DOCX, RTF, or TXT file",
    ),
});

const Step2 = z.object({
  preferredRegions: z.array(z.string()).min(1, "Pick at least one region"),
  country: z.string().min(2, "Enter your country"),
  preferredTimezones: z.array(z.string()),
  preferredLanguages: z.array(z.string()).min(1, "Pick at least one language"),
  jobTypes: z.array(z.string()).min(1, "Pick at least one job type"),
});

const Step3 = z.object({
  plan: z.enum(["starter", "pro", "managed"]),
  agreeTerms: z.literal(true, {
    errorMap: () => ({ message: "Please agree to the Terms before finishing" }),
  }),
});

// Zod infers cv as File from the `.refine(v => v instanceof File)` chain.
// But we also need to allow clearing the field (setValue("cv", undefined)),
// so widen it to File | undefined explicitly.
type FormValues =
  Omit<z.infer<typeof Step1>, "cv"> & { cv?: File } &
  z.infer<typeof Step2> &
  z.infer<typeof Step3>;

const STEP_LABELS = ["About you", "Your preferences", "Choose a plan"] as const;

const REGIONS = [
  "Anywhere",
  "Africa",
  "Europe",
  "North America",
  "South America",
  "Asia",
  "Oceania",
];
const TIMEZONES = [
  "EAT (UTC+3)",
  "WAT (UTC+1)",
  "CAT (UTC+2)",
  "SAST (UTC+2)",
  "GMT (UTC+0)",
  "CET (UTC+1)",
  "EST (UTC-5)",
  "PST (UTC-8)",
];

const LANGUAGES = [
  "English",
  "French",
  "Arabic",
  "Swahili",
  "Portuguese",
  "Spanish",
  "German",
  "Mandarin",
];

const JOB_TYPES = [
  "Full-time",
  "Part-time",
  "Contract",
  "Freelance",
  "Internship",
];

function readPlanFromQuery(): PlanId {
  if (typeof window === "undefined") return "starter";
  const p = new URL(window.location.href).searchParams.get("plan");
  if (p === "starter" || p === "pro" || p === "managed") return p;
  return "starter";
}

export default function Onboarding() {
  const { state, login } = useAuth();
  const [step, setStep] = useState<1 | 2 | 3>(1);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const initialPlan = useMemo(readPlanFromQuery, []);
  const form = useForm<FormValues>({
    defaultValues: {
      targetJobTitle: "",
      experienceLevel: "mid",
      jobSearchStatus: "actively_looking",
      wantsATSReport: true,
      preferredRegions: [],
      preferredTimezones: [],
      preferredLanguages: ["English"],
      jobTypes: ["Full-time"],
      country: "",
      plan: initialPlan,
      agreeTerms: false as unknown as true,
      cv: undefined,
    },
    mode: "onBlur",
  });

  if (state === "unauthenticated") return <SignInPrompt onSignIn={login} />;

  const onSubmit: SubmitHandler<FormValues> = async (data) => {
    setSubmitting(true);
    setSubmitError(null);
    try {
      const res = await submitOnboarding({
        target_job_title:    data.targetJobTitle,
        experience_level:    data.experienceLevel,
        job_search_status:   data.jobSearchStatus,
        salary_range:        data.salaryRange ?? "",
        wants_ats_report:    data.wantsATSReport,
        preferred_regions:   data.preferredRegions,
        preferred_timezones: data.preferredTimezones,
        preferred_languages: data.preferredLanguages,
        job_types:           data.jobTypes,
        country:             data.country,
        plan:                data.plan,
        agree_terms:         data.agreeTerms,
        cv:                  data.cv instanceof File ? data.cv : null,
      });
      if (!res.ok) {
        const body = await res.text().catch(() => "");
        throw new Error(body.slice(0, 200) || res.statusText);
      }
      // Free → dashboard. Paid → payment flow (stubbed): land on dashboard
      // which prompts to complete payment. Backend issues the checkout URL
      // in the response body; when that's wired we'll redirect there.
      window.location.href = "/dashboard/";
    } catch (e) {
      setSubmitError(
        e instanceof Error && e.message
          ? `We couldn't save your profile: ${e.message}`
          : "We couldn't save your profile. Please try again or contact hello@stawi.org.",
      );
    } finally {
      setSubmitting(false);
    }
  };

  async function next() {
    let schema: z.ZodTypeAny = Step1;
    if (step === 2) schema = Step2;
    if (step === 3) schema = Step3;
    const parsed = schema.safeParse(form.getValues());
    if (!parsed.success) {
      for (const issue of parsed.error.issues) {
        form.setError(issue.path[0] as keyof FormValues, { message: issue.message });
      }
      return;
    }
    if (step < 3) setStep((s) => (s + 1) as 1 | 2 | 3);
    else await form.handleSubmit(onSubmit)();
  }

  const selectedPlan = form.watch("plan");
  const finishLabel = step === 3
    ? `Continue to payment · $${planById(selectedPlan).price}/mo`
    : "Continue";

  return (
    <div className="mx-auto max-w-4xl px-4 py-8 sm:px-6 lg:px-8">
      <Progress step={step} />
      <form
        className="mt-8"
        onSubmit={(e) => {
          e.preventDefault();
          void next();
        }}
      >
        {step === 1 && <Step1Form form={form} />}
        {step === 2 && <Step2Form form={form} />}
        {step === 3 && <Step3Form form={form} />}
        {submitError && (
          <p
            className="mt-4 rounded bg-red-50 px-3 py-2 text-sm text-red-700"
            role="alert"
          >
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
            Back
          </button>
          <button
            type="submit"
            disabled={submitting}
            className="rounded bg-navy-900 px-6 py-2.5 text-sm font-medium text-white shadow-sm hover:bg-navy-800 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-navy-900 disabled:opacity-60"
          >
            {submitting ? "Submitting…" : finishLabel}
          </button>
        </div>
      </form>
    </div>
  );
}

function Progress({ step }: { step: 1 | 2 | 3 }) {
  return (
    <div
      role="progressbar"
      aria-label="Onboarding progress"
      aria-valuemin={1}
      aria-valuemax={3}
      aria-valuenow={step}
    >
      <p className="text-xs font-semibold uppercase tracking-wide text-gray-500">
        Step {step} of 3 · {STEP_LABELS[step - 1]}
      </p>
      <ol className="mt-3 grid grid-cols-3 gap-2" aria-hidden>
        {STEP_LABELS.map((label, i) => {
          const n = (i + 1) as 1 | 2 | 3;
          const done = step > n;
          const active = step === n;
          return (
            <li key={label} className="flex flex-col gap-1">
              <div
                className={`h-1.5 rounded-full transition-colors ${
                  done || active ? "bg-accent-500" : "bg-gray-200"
                }`}
              />
              <span
                className={`text-xs ${
                  active ? "font-medium text-gray-900" : "text-gray-500"
                }`}
              >
                {label}
              </span>
            </li>
          );
        })}
      </ol>
    </div>
  );
}

type FormProps = { form: UseFormReturn<FormValues> };

function Step1Form({ form }: FormProps) {
  const {
    register,
    watch,
    setValue,
    formState: { errors },
  } = form;
  const cv = watch("cv") as File | undefined;
  return (
    <div className="space-y-6">
      <header>
        <h1 className="text-3xl font-bold text-gray-900">About you</h1>
        <p className="mt-1 text-gray-600">
          Tell us what you're looking for so we can surface the most relevant
          roles.
        </p>
      </header>
      <Field label="Target job title" error={errors.targetJobTitle?.message}>
        {(id) => (
          <input
            id={id}
            type="text"
            autoComplete="organization-title"
            placeholder="e.g. Senior Software Engineer"
            {...register("targetJobTitle")}
            className="w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-navy-900 focus:outline-none focus:ring-1 focus:ring-navy-900"
          />
        )}
      </Field>
      <Field label="Experience level">
        {(id) => (
          <select
            id={id}
            {...register("experienceLevel")}
            className="w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-navy-900 focus:outline-none focus:ring-1 focus:ring-navy-900"
          >
            <option value="entry">Entry (0–2 years)</option>
            <option value="junior">Junior (2–4 years)</option>
            <option value="mid">Mid-level (4–6 years)</option>
            <option value="senior">Senior (6–10 years)</option>
            <option value="lead">Lead (10+ years)</option>
            <option value="executive">Executive</option>
          </select>
        )}
      </Field>
      <Field label="Job search status">
        {(id) => (
          <select
            id={id}
            {...register("jobSearchStatus")}
            className="w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-navy-900 focus:outline-none focus:ring-1 focus:ring-navy-900"
          >
            <option value="actively_looking">Actively looking</option>
            <option value="open_to_offers">Open to offers</option>
            <option value="casually_browsing">Casually browsing</option>
          </select>
        )}
      </Field>
      <Field label="Target salary (optional)">
        {(id) => (
          <select
            id={id}
            {...register("salaryRange")}
            className="w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-navy-900 focus:outline-none focus:ring-1 focus:ring-navy-900"
          >
            <option value="">Prefer not to say</option>
            <option value="30k-50k">$30,000 – $50,000</option>
            <option value="50k-75k">$50,000 – $75,000</option>
            <option value="75k-100k">$75,000 – $100,000</option>
            <option value="100k+">$100,000+</option>
          </select>
        )}
      </Field>
      <Field
        label="Upload your CV (optional)"
        error={errors.cv?.message as string | undefined}
      >
        {(id) => (
          <div className="rounded-lg border-2 border-dashed border-gray-300 bg-gray-50 p-4">
            {cv ? (
              <div className="flex items-center justify-between gap-3">
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium text-gray-900">
                    {cv.name}
                  </p>
                  <p className="text-xs text-gray-500">
                    {(cv.size / 1024).toFixed(1)} KB · ready to upload
                  </p>
                </div>
                <button
                  type="button"
                  onClick={() => setValue("cv", undefined, { shouldValidate: true })}
                  className="text-sm font-medium text-gray-600 hover:text-gray-900"
                >
                  Remove
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
                    setValue("cv", f ?? undefined, { shouldValidate: true });
                  }}
                  className="sr-only"
                />
                <label
                  htmlFor={id}
                  className="inline-flex cursor-pointer items-center rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-700 shadow-sm hover:bg-gray-50"
                >
                  <svg className="mr-2 h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" aria-hidden="true">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M7 16a4 4 0 01-.88-7.903A5 5 0 0115.9 6L16 6a5 5 0 011 9.9M15 13l-3-3m0 0l-3 3m3-3v12" />
                  </svg>
                  Choose file
                </label>
                <p className="mt-2 text-xs text-gray-500">
                  PDF, DOCX, RTF, or TXT · up to 10 MB
                </p>
              </div>
            )}
          </div>
        )}
      </Field>
      <p className="text-xs text-gray-500">
        Your CV is used to match you with relevant jobs. It's never shared
        with employers without your action — every application goes through
        you (or your agent, on Managed).
      </p>
      <label className="flex items-start gap-3 rounded-md border border-gray-200 p-3">
        <input
          type="checkbox"
          {...register("wantsATSReport")}
          className="mt-0.5 h-4 w-4 rounded border-gray-300 text-navy-900 focus:ring-navy-900"
        />
        <span className="text-sm text-gray-700">
          Email me a free resume score (ATS-compatibility check). We scan for
          common formatting issues hiring software rejects.
        </span>
      </label>
    </div>
  );
}

function Step2Form({ form }: FormProps) {
  const {
    register,
    watch,
    setValue,
    formState: { errors },
  } = form;
  const selectedRegions = watch("preferredRegions");
  const selectedTZ = watch("preferredTimezones");
  const selectedLangs = watch("preferredLanguages");
  const selectedJobTypes = watch("jobTypes");
  const anywhereSelected = selectedRegions.includes("Anywhere");

  function toggleLang(lang: string) {
    const on = selectedLangs.includes(lang);
    setValue(
      "preferredLanguages",
      on ? selectedLangs.filter((x) => x !== lang) : [...selectedLangs, lang],
      { shouldValidate: true },
    );
  }
  function toggleJobType(t: string) {
    const on = selectedJobTypes.includes(t);
    setValue(
      "jobTypes",
      on ? selectedJobTypes.filter((x) => x !== t) : [...selectedJobTypes, t],
      { shouldValidate: true },
    );
  }

  function toggleRegion(r: string) {
    if (r === "Anywhere") {
      setValue("preferredRegions", anywhereSelected ? [] : ["Anywhere"], {
        shouldValidate: true,
      });
      return;
    }
    const withoutAnywhere = selectedRegions.filter((x) => x !== "Anywhere");
    const on = withoutAnywhere.includes(r);
    setValue(
      "preferredRegions",
      on ? withoutAnywhere.filter((x) => x !== r) : [...withoutAnywhere, r],
      { shouldValidate: true },
    );
  }

  return (
    <div className="space-y-6">
      <header>
        <h1 className="text-3xl font-bold text-gray-900">Your preferences</h1>
        <p className="mt-1 text-gray-600">
          We'll filter out roles that don't match your location and timezone.
        </p>
      </header>
      <Field
        label="Regions you're able to work in"
        error={errors.preferredRegions?.message as string | undefined}
      >
        {() => (
          <div className="flex flex-wrap gap-2" role="group" aria-label="Regions">
            {REGIONS.map((r) => {
              const on = selectedRegions.includes(r);
              return (
                <button
                  key={r}
                  type="button"
                  aria-pressed={on}
                  className={`min-h-[44px] rounded-full border px-4 py-2 text-sm font-medium transition-colors ${
                    on
                      ? "border-navy-900 bg-navy-900 text-white"
                      : "border-gray-200 bg-white text-gray-700 hover:border-gray-300 hover:bg-gray-50"
                  }`}
                  onClick={() => toggleRegion(r)}
                >
                  {r}
                </button>
              );
            })}
          </div>
        )}
      </Field>
      <Field label="Preferred time zones (optional)">
        {() => (
          <div className="flex flex-wrap gap-2" role="group" aria-label="Time zones">
            {TIMEZONES.map((tz) => {
              const on = selectedTZ.includes(tz);
              return (
                <button
                  key={tz}
                  type="button"
                  aria-pressed={on}
                  className={`min-h-[44px] rounded-full border px-4 py-2 text-sm font-medium transition-colors ${
                    on
                      ? "border-navy-900 bg-navy-900 text-white"
                      : "border-gray-200 bg-white text-gray-700 hover:border-gray-300 hover:bg-gray-50"
                  }`}
                  onClick={() =>
                    setValue(
                      "preferredTimezones",
                      on
                        ? selectedTZ.filter((x) => x !== tz)
                        : [...selectedTZ, tz],
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
        label="Languages you work in"
        error={errors.preferredLanguages?.message as string | undefined}
      >
        {() => (
          <div className="flex flex-wrap gap-2" role="group" aria-label="Languages">
            {LANGUAGES.map((lang) => {
              const on = selectedLangs.includes(lang);
              return (
                <button
                  key={lang}
                  type="button"
                  aria-pressed={on}
                  className={`min-h-[44px] rounded-full border px-4 py-2 text-sm font-medium transition-colors ${
                    on
                      ? "border-navy-900 bg-navy-900 text-white"
                      : "border-gray-200 bg-white text-gray-700 hover:border-gray-300 hover:bg-gray-50"
                  }`}
                  onClick={() => toggleLang(lang)}
                >
                  {lang}
                </button>
              );
            })}
          </div>
        )}
      </Field>
      <Field
        label="Job type"
        error={errors.jobTypes?.message as string | undefined}
      >
        {() => (
          <div className="flex flex-wrap gap-2" role="group" aria-label="Job type">
            {JOB_TYPES.map((t) => {
              const on = selectedJobTypes.includes(t);
              return (
                <button
                  key={t}
                  type="button"
                  aria-pressed={on}
                  className={`min-h-[44px] rounded-full border px-4 py-2 text-sm font-medium transition-colors ${
                    on
                      ? "border-navy-900 bg-navy-900 text-white"
                      : "border-gray-200 bg-white text-gray-700 hover:border-gray-300 hover:bg-gray-50"
                  }`}
                  onClick={() => toggleJobType(t)}
                >
                  {t}
                </button>
              );
            })}
          </div>
        )}
      </Field>
      <Field label="Country" error={errors.country?.message}>
        {(id) => (
          <input
            id={id}
            type="text"
            autoComplete="country-name"
            placeholder="e.g. Kenya"
            {...register("country")}
            className="w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-navy-900 focus:outline-none focus:ring-1 focus:ring-navy-900"
          />
        )}
      </Field>
    </div>
  );
}

function Step3Form({ form }: FormProps) {
  const {
    register,
    watch,
    setValue,
    formState: { errors },
  } = form;
  const plan = watch("plan");

  return (
    <div className="space-y-6">
      <header>
        <h1 className="text-3xl font-bold text-gray-900">Choose your plan</h1>
        <p className="mt-1 text-gray-600">
          You can upgrade or cancel any time. All plans include weekly
          matches to your inbox.
        </p>
      </header>

      <Field error={errors.plan?.message as string | undefined}>
        {() => (
          <div className="grid gap-3 sm:grid-cols-3" role="radiogroup" aria-label="Plan">
            {PLANS.map((p) => {
              const on = plan === p.id;
              const priceLabel = `$${p.price}/mo`;
              return (
                <button
                  key={p.id}
                  type="button"
                  role="radio"
                  aria-checked={on}
                  onClick={() => setValue("plan", p.id, { shouldValidate: true })}
                  className={`flex flex-col items-start gap-1 rounded-lg border-2 p-4 text-left transition-colors ${
                    on
                      ? "border-accent-500 bg-accent-50"
                      : "border-gray-200 bg-white hover:border-gray-300"
                  }`}
                >
                  <div className="flex w-full items-center justify-between gap-2">
                    <span className="text-base font-semibold text-gray-900">
                      {p.name}
                    </span>
                    <span className="text-sm font-semibold text-gray-900">
                      {priceLabel}
                    </span>
                  </div>
                  <p className="text-sm text-gray-600">{p.tagline}</p>
                  {p.matchesPerWeek !== null && (
                    <p className="mt-1 text-xs text-gray-500">
                      Up to {p.matchesPerWeek} matches per week
                    </p>
                  )}
                  {p.meta.agent && (
                    <p className="mt-1 text-xs font-medium text-accent-700">
                      Includes a dedicated agent
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
              {...register("agreeTerms")}
              className="mt-0.5 h-4 w-4 rounded border-gray-300 text-navy-900 focus:ring-navy-900"
            />
            <span className="text-sm text-gray-700">
              I agree to the{" "}
              <a href="/terms/" className="font-medium text-accent-600 underline hover:text-accent-700">
                Terms
              </a>{" "}
              and{" "}
              <a href="/privacy/" className="font-medium text-accent-600 underline hover:text-accent-700">
                Privacy Policy
              </a>
              .
            </span>
          </label>
        )}
      </Field>

      <p className="text-xs text-gray-500">
        You'll be redirected to our payment partner to complete the
        subscription. Cancel any time from your dashboard.
      </p>
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
      <div className={label ? "mt-1" : ""}>{children(id)}</div>
      {error && (
        <p className="mt-1 text-sm text-red-600" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}

function SignInPrompt({ onSignIn }: { onSignIn: () => Promise<void> }) {
  return (
    <div className="mx-auto max-w-md py-16 text-center">
      <h1 className="text-2xl font-semibold text-gray-900">
        Sign in to get started
      </h1>
      <p className="mt-2 text-gray-600">
        We'll save your preferences and surface matching roles on your
        dashboard.
      </p>
      <button
        type="button"
        onClick={() => void onSignIn()}
        className="mt-6 rounded-md bg-navy-900 px-6 py-2.5 text-sm font-medium text-white shadow-sm hover:bg-navy-800 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-navy-900"
      >
        Sign in
      </button>
    </div>
  );
}
