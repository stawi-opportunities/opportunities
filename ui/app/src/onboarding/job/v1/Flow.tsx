// job-onboarding-v1 — first kind-specific onboarding flow.
//
// Owns the shape posted under opt_ins["job"] in the polymorphic
// PreferencesUpdatedV1 envelope (Phase 7.6). The job matcher in
// apps/matching reads this same shape — see
// apps/matching/business/match/job/matcher.go for what each field
// means at scoring time.

import { useState, type ChangeEvent } from "react";
import {
  LocationPicker,
  emptyLocationPreference,
  type LocationPreference,
} from "@/onboarding/shared/LocationPicker";
import { AmountRange } from "@/onboarding/shared/AmountRange";

export interface JobPreferences {
  target_roles: string[];
  employment_types: string[];
  seniority_levels: string[];
  salary_min: number;
  salary_max: number;
  currency: string;
  locations: LocationPreference;
}

const EMPTY: JobPreferences = {
  target_roles: [],
  employment_types: [],
  seniority_levels: [],
  salary_min: 0,
  salary_max: 0,
  currency: "USD",
  locations: emptyLocationPreference,
};

const EMPLOYMENT_TYPES = ["full-time", "part-time", "contract", "internship", "freelance"];

export function Flow({
  initial,
  onSubmit,
}: {
  initial?: JobPreferences;
  onSubmit: (p: JobPreferences) => void;
}) {
  const [p, setP] = useState<JobPreferences>(initial ?? EMPTY);
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit(p);
      }}
      className="space-y-6 max-w-xl"
    >
      <h2 className="text-2xl font-semibold text-gray-900">Tell us about your job search</h2>
      <input
        type="text"
        placeholder="Target roles (comma-separated)"
        value={p.target_roles.join(", ")}
        onChange={(e: ChangeEvent<HTMLInputElement>) =>
          setP({
            ...p,
            target_roles: e.currentTarget.value
              .split(",")
              .map((s) => s.trim())
              .filter(Boolean),
          })
        }
        className="input-field"
      />
      <fieldset>
        <legend className="text-sm font-medium text-gray-700">Employment type</legend>
        <div className="mt-2 flex flex-wrap gap-x-4 gap-y-2">
          {EMPLOYMENT_TYPES.map((t) => (
            <label className="inline-flex items-center gap-2 text-sm text-gray-700" key={t}>
              <input
                type="checkbox"
                checked={p.employment_types.includes(t)}
                onChange={(e: ChangeEvent<HTMLInputElement>) => {
                  const checked = e.currentTarget.checked;
                  setP({
                    ...p,
                    employment_types: checked
                      ? [...p.employment_types, t]
                      : p.employment_types.filter((x) => x !== t),
                  });
                }}
                className="h-4 w-4 rounded border-gray-300 text-navy-900 focus:ring-navy-900"
              />
              {t}
            </label>
          ))}
        </div>
      </fieldset>
      <AmountRange
        label="Salary range"
        currency={p.currency}
        amountMin={p.salary_min}
        amountMax={p.salary_max}
        onAmountMin={(v) => setP({ ...p, salary_min: v })}
        onAmountMax={(v) => setP({ ...p, salary_max: v })}
        onCurrency={(c) => setP({ ...p, currency: c })}
      />
      <LocationPicker value={p.locations} onChange={(l) => setP({ ...p, locations: l })} />
      <button type="submit" className="btn-primary">
        Save preferences
      </button>
    </form>
  );
}

export default Flow;
