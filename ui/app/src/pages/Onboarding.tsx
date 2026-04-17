import { useId, useState } from "react";
import { useForm, type SubmitHandler, type UseFormReturn } from "react-hook-form";
import { z } from "zod";
import { useAuth } from "@/providers/AuthProvider";
import { submitOnboarding } from "@/api/candidates";

// Each step owns a contiguous slice of fields; we validate one step at a
// time before advancing so errors surface close to the inputs the user
// just touched.

const Step1 = z.object({
  targetJobTitle: z.string().min(2, "Enter a target job title"),
  experienceLevel: z.enum(["entry", "junior", "mid", "senior", "lead", "executive"]),
  jobSearchStatus: z.enum(["actively_looking", "open_to_offers", "casually_browsing"]),
  salaryRange: z.string().optional(),
  wantsATSReport: z.boolean(),
});

const Step2 = z.object({
  preferredRegions: z.array(z.string()).min(1, "Pick at least one region"),
  country: z.string().min(2, "Enter your country"),
  preferredTimezones: z.array(z.string()),
});

const Step3 = z.object({
  paymentMethod: z.enum(["card", "mobile"]).optional(),
  agreeTerms: z.literal(true, {
    errorMap: () => ({ message: "Please agree to the Terms before finishing" }),
  }),
});

type FormValues = z.infer<typeof Step1> & z.infer<typeof Step2> & z.infer<typeof Step3>;

const STEP_LABELS = ["About you", "Your preferences", "Review"] as const;

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

export default function Onboarding() {
  const { state, login } = useAuth();
  const [step, setStep] = useState<1 | 2 | 3>(1);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const form = useForm<FormValues>({
    defaultValues: {
      targetJobTitle: "",
      experienceLevel: "mid",
      jobSearchStatus: "actively_looking",
      wantsATSReport: true,
      preferredRegions: [],
      preferredTimezones: [],
      country: "",
      agreeTerms: false as unknown as true,
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
        country:             data.country,
        payment_method:      data.paymentMethod ?? "",
        agree_terms:         data.agreeTerms,
      });
      if (!res.ok) {
        const body = await res.text().catch(() => "");
        throw new Error(body.slice(0, 200) || res.statusText);
      }
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
            className="rounded bg-navy-900 px-6 py-2.5 text-sm font-medium text-white shadow-sm hover:bg-navy-800 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-500 disabled:opacity-60"
          >
            {submitting ? "Submitting…" : step === 3 ? "Finish" : "Continue"}
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
    formState: { errors },
  } = form;
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
            className="w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-accent-500 focus:outline-none focus:ring-1 focus:ring-accent-500"
          />
        )}
      </Field>
      <Field label="Experience level">
        {(id) => (
          <select
            id={id}
            {...register("experienceLevel")}
            className="w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-accent-500 focus:outline-none focus:ring-1 focus:ring-accent-500"
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
            className="w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-accent-500 focus:outline-none focus:ring-1 focus:ring-accent-500"
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
            className="w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-accent-500 focus:outline-none focus:ring-1 focus:ring-accent-500"
          >
            <option value="">Prefer not to say</option>
            <option value="30k-50k">$30,000 – $50,000</option>
            <option value="50k-75k">$50,000 – $75,000</option>
            <option value="75k-100k">$75,000 – $100,000</option>
            <option value="100k+">$100,000+</option>
          </select>
        )}
      </Field>
      <label className="flex items-start gap-3 rounded-md border border-gray-200 p-3">
        <input
          type="checkbox"
          {...register("wantsATSReport")}
          className="mt-0.5 h-4 w-4 rounded border-gray-300 text-accent-500 focus:ring-accent-500"
        />
        <span className="text-sm text-gray-700">
          Email me a free resume score (ATS-compatibility check). We scan for
          common formatting issues that hiring-software rejects.
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
  const anywhereSelected = selectedRegions.includes("Anywhere");

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
      <Field label="Country" error={errors.country?.message}>
        {(id) => (
          <input
            id={id}
            type="text"
            autoComplete="country-name"
            placeholder="e.g. Kenya"
            {...register("country")}
            className="w-full rounded-md border border-gray-300 px-3 py-2 shadow-sm focus:border-accent-500 focus:outline-none focus:ring-1 focus:ring-accent-500"
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
  const method = watch("paymentMethod");
  return (
    <div className="space-y-6">
      <header>
        <h1 className="text-3xl font-bold text-gray-900">Almost done</h1>
        <p className="mt-1 text-gray-600">
          Start free — browse jobs, save favourites, and receive matches.
          Upgrade any time for priority matching and weekly email digests.
        </p>
      </header>
      <div className="rounded-lg border border-gray-200 bg-gray-50 p-6">
        <div className="flex items-baseline justify-between gap-4">
          <div>
            <p className="text-sm font-medium text-gray-900">Free</p>
            <p className="text-sm text-gray-600">Browse, save, and get matched.</p>
          </div>
          <p className="text-lg font-semibold text-gray-900">$0</p>
        </div>
        <div className="mt-4 flex items-baseline justify-between gap-4 border-t border-gray-200 pt-4">
          <div>
            <p className="text-sm font-medium text-gray-900">
              Premium{" "}
              <span className="ml-1 rounded-full bg-accent-50 px-2 py-0.5 text-xs font-medium text-accent-700">
                Optional
              </span>
            </p>
            <p className="text-sm text-gray-600">
              Priority matching, weekly digest, resume review.
            </p>
          </div>
          <p className="text-sm text-gray-700">
            <span className="line-through">$14.95</span>{" "}
            <span className="font-semibold text-gray-900">$2.95</span>
            <span className="text-gray-500"> first month</span>
          </p>
        </div>
        <a
          href="/pricing/"
          className="mt-3 inline-block text-sm font-medium text-accent-600 hover:text-accent-700"
        >
          Compare plans →
        </a>
      </div>
      <Field label="Add payment now (optional)">
        {() => (
          <div
            className="flex flex-col gap-3 sm:flex-row"
            role="group"
            aria-label="Payment method"
          >
            {(["card", "mobile"] as const).map((m) => (
              <button
                key={m}
                type="button"
                aria-pressed={method === m}
                className={`min-h-[44px] flex-1 rounded-md border px-4 py-3 text-sm font-medium transition-colors ${
                  method === m
                    ? "border-accent-500 bg-accent-50 text-accent-700"
                    : "border-gray-200 bg-white text-gray-700 hover:border-gray-300 hover:bg-gray-50"
                }`}
                onClick={() =>
                  setValue("paymentMethod", method === m ? undefined : m)
                }
              >
                {m === "card" ? "Card" : "Mobile money"}
              </button>
            ))}
          </div>
        )}
      </Field>
      <Field error={errors.agreeTerms?.message as string | undefined}>
        {() => (
          <label className="flex items-start gap-3">
            <input
              type="checkbox"
              {...register("agreeTerms")}
              className="mt-0.5 h-4 w-4 rounded border-gray-300 text-accent-500 focus:ring-accent-500"
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
        className="mt-6 rounded-md bg-navy-900 px-6 py-2.5 text-sm font-medium text-white shadow-sm hover:bg-navy-800 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-500"
      >
        Sign in
      </button>
    </div>
  );
}
