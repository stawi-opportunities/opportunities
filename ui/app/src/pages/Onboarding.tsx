import { useState } from "react";
import { useForm, type SubmitHandler, type UseFormReturn } from "react-hook-form";
import { z } from "zod";
import { useAuth } from "@/providers/AuthProvider";

// Zod schema mirrors the Alpine wizard's validation. Each step owns a
// contiguous field set; we validate one step at a time before advancing.

const Step1 = z.object({
  targetJobTitle: z.string().min(2, "Required"),
  experienceLevel: z.enum(["entry", "junior", "mid", "senior", "lead", "executive"]),
  jobSearchStatus: z.enum(["actively_looking", "open_to_offers", "casually_browsing"]),
  salaryRange: z.string().optional(),
  wantsATSReport: z.boolean(),
});

const Step2 = z.object({
  preferredRegions: z.array(z.string()).min(1, "Pick at least one region"),
  country: z.string().min(2, "Required"),
  preferredTimezones: z.array(z.string()),
});

const Step3 = z.object({
  paymentMethod: z.enum(["card", "mobile"]).optional(),
  agreeTerms: z.boolean(),
});

type FormValues = z.infer<typeof Step1> & z.infer<typeof Step2> & z.infer<typeof Step3>;

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
  const { state } = useAuth();
  const [step, setStep] = useState<1 | 2 | 3>(1);
  const form = useForm<FormValues>({
    defaultValues: {
      targetJobTitle: "",
      experienceLevel: "mid",
      jobSearchStatus: "actively_looking",
      wantsATSReport: true,
      preferredRegions: [],
      preferredTimezones: [],
      country: "",
      agreeTerms: false,
    },
    mode: "onBlur",
  });

  if (state === "unauthenticated") return <SignInPrompt />;

  const onSubmit: SubmitHandler<FormValues> = async (data) => {
    console.log("onboarding submit", data);
    window.location.href = "/dashboard/";
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
      <form className="mt-8" onSubmit={(e) => { e.preventDefault(); void next(); }}>
        {step === 1 && <Step1Form form={form} />}
        {step === 2 && <Step2Form form={form} />}
        {step === 3 && <Step3Form form={form} />}
        <div className="mt-8 flex justify-between">
          <button
            type="button"
            disabled={step === 1}
            onClick={() => setStep((s) => Math.max(1, s - 1) as 1 | 2 | 3)}
            className="rounded border border-gray-300 px-4 py-2 text-sm disabled:opacity-50"
          >
            Back
          </button>
          <button type="submit" className="rounded bg-navy-900 px-5 py-2 text-sm text-white hover:bg-navy-800">
            {step === 3 ? "Finish" : "Continue"}
          </button>
        </div>
      </form>
    </div>
  );
}

function Progress({ step }: { step: 1 | 2 | 3 }) {
  return (
    <div role="progressbar" aria-valuemin={1} aria-valuemax={3} aria-valuenow={step}>
      <p className="text-sm font-semibold text-gray-900">STEP {step} OF 3</p>
      <div className="mt-2 flex gap-1">
        {[1, 2, 3].map((n) => (
          <div key={n} className={`h-1.5 flex-1 rounded-full ${step >= n ? "bg-accent-500" : "bg-gray-200"}`} />
        ))}
      </div>
    </div>
  );
}

type FormProps = { form: UseFormReturn<FormValues> };

function Step1Form({ form }: FormProps) {
  const { register, formState: { errors } } = form;
  return (
    <div className="space-y-6">
      <h1 className="text-3xl font-bold text-gray-900">About you</h1>
      <p className="text-gray-600">Tell us about yourself so companies know who you are.</p>
      <Field label="Target job title" error={errors.targetJobTitle?.message}>
        <input type="text" {...register("targetJobTitle")} className="w-full rounded border border-gray-300 px-3 py-2" />
      </Field>
      <Field label="Experience level">
        <select {...register("experienceLevel")} className="w-full rounded border border-gray-300 px-3 py-2">
          <option value="entry">Entry (0-2 years)</option>
          <option value="junior">Junior (2-4 years)</option>
          <option value="mid">Mid-level (4-6 years)</option>
          <option value="senior">Senior (6-10 years)</option>
          <option value="lead">Lead (10+ years)</option>
          <option value="executive">Executive</option>
        </select>
      </Field>
      <Field label="Job search status">
        <select {...register("jobSearchStatus")} className="w-full rounded border border-gray-300 px-3 py-2">
          <option value="actively_looking">Actively looking</option>
          <option value="open_to_offers">Open to offers</option>
          <option value="casually_browsing">Casually browsing</option>
        </select>
      </Field>
      <Field label="Salary range">
        <select {...register("salaryRange")} className="w-full rounded border border-gray-300 px-3 py-2">
          <option value="">Select…</option>
          <option value="30k-50k">$30,000-$50,000</option>
          <option value="50k-75k">$50,000-$75,000</option>
          <option value="75k-100k">$75,000-$100,000</option>
          <option value="100k+">$100,000+</option>
        </select>
      </Field>
      <label className="flex items-center gap-2">
        <input type="checkbox" {...register("wantsATSReport")} />
        <span className="text-sm text-gray-700">Email me a free ATS resume score</span>
      </label>
    </div>
  );
}

function Step2Form({ form }: FormProps) {
  const { register, watch, setValue, formState: { errors } } = form;
  const selectedRegions = watch("preferredRegions");
  const selectedTZ = watch("preferredTimezones");
  return (
    <div className="space-y-6">
      <h1 className="text-3xl font-bold text-gray-900">Curate your search</h1>
      <Field label="Regions you're able to work in" error={errors.preferredRegions?.message as string | undefined}>
        <div className="flex flex-wrap gap-2">
          {REGIONS.map((r) => {
            const on = selectedRegions.includes(r);
            return (
              <button
                key={r}
                type="button"
                className={`rounded-full px-3 py-1.5 text-sm ${on ? "bg-navy-900 text-white" : "bg-gray-100 text-gray-700"}`}
                onClick={() =>
                  setValue("preferredRegions", on ? selectedRegions.filter((x) => x !== r) : [...selectedRegions, r], { shouldValidate: true })
                }
              >
                {r}
              </button>
            );
          })}
        </div>
      </Field>
      <Field label="Preferred time zones">
        <div className="flex flex-wrap gap-2">
          {TIMEZONES.map((tz) => {
            const on = selectedTZ.includes(tz);
            return (
              <button
                key={tz}
                type="button"
                className={`rounded-full px-3 py-1.5 text-sm ${on ? "bg-navy-900 text-white" : "bg-gray-100 text-gray-700"}`}
                onClick={() =>
                  setValue("preferredTimezones", on ? selectedTZ.filter((x) => x !== tz) : [...selectedTZ, tz])
                }
              >
                {tz}
              </button>
            );
          })}
        </div>
      </Field>
      <Field label="Country" error={errors.country?.message}>
        <input type="text" {...register("country")} className="w-full rounded border border-gray-300 px-3 py-2" placeholder="e.g. Kenya" />
      </Field>
    </div>
  );
}

function Step3Form({ form }: FormProps) {
  const { register, watch, setValue } = form;
  const method = watch("paymentMethod");
  return (
    <div className="space-y-6">
      <h1 className="text-3xl font-bold text-gray-900">Get full access</h1>
      <div className="rounded-lg border border-gray-200 p-6">
        <p className="text-sm text-gray-500 line-through">$14.95/month</p>
        <p className="text-3xl font-bold text-gray-900">
          $2.95 <span className="text-base font-normal text-gray-500">/first month</span>
        </p>
        <p className="mt-2 text-sm text-accent-600">Unlock everything for $2.95 your first month, then $14.95/month.</p>
      </div>
      <Field label="Payment method">
        <div className="flex gap-3">
          {(["card", "mobile"] as const).map((m) => (
            <button
              key={m}
              type="button"
              className={`flex-1 rounded border px-4 py-3 text-sm font-medium ${method === m ? "border-accent-500 bg-accent-50" : "border-gray-200"}`}
              onClick={() => setValue("paymentMethod", m)}
            >
              {m === "card" ? "Card" : "Mobile money"}
            </button>
          ))}
        </div>
      </Field>
      <label className="flex items-start gap-2">
        <input type="checkbox" {...register("agreeTerms")} className="mt-0.5" />
        <span className="text-sm text-gray-600">
          I agree to the <a href="/terms/" className="text-accent-600 underline">Terms</a>.
        </span>
      </label>
    </div>
  );
}

function Field({ label, error, children }: { label: string; error?: string; children: React.ReactNode }) {
  return (
    <div>
      <label className="block text-sm font-medium text-gray-700">{label}</label>
      <div className="mt-1">{children}</div>
      {error && <p className="mt-1 text-sm text-red-600">{error}</p>}
    </div>
  );
}

function SignInPrompt() {
  return (
    <div className="mx-auto max-w-md py-16 text-center">
      <h1 className="text-2xl font-semibold text-gray-900">Sign in to get started</h1>
      <p className="mt-2 text-gray-600">Use the Sign In button in the top right.</p>
    </div>
  );
}
