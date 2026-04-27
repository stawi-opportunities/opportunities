// deal-onboarding-v1 — interest categories + country filter for the
// deal matcher. Categories drive topical relevance; countries scope
// availability (some deals are geo-fenced).

import { useState, type ChangeEvent, type FormEvent } from "react";

export interface DealPreferences {
  interest_categories: string[];
  countries: string[];
}

const EMPTY: DealPreferences = {
  interest_categories: [],
  countries: [],
};

const CATEGORIES = ["Software", "Hardware", "Travel", "Education", "Subscriptions"];

export function Flow({
  initial,
  onSubmit,
}: {
  initial?: DealPreferences;
  onSubmit: (p: DealPreferences) => void;
}) {
  const [p, setP] = useState<DealPreferences>(initial ?? EMPTY);
  const submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    onSubmit(p);
  };
  return (
    <form onSubmit={submit} className="space-y-4 max-w-xl">
      <h2 className="text-2xl font-semibold text-gray-900">What kinds of deals interest you?</h2>
      <fieldset>
        <legend className="text-sm font-medium text-gray-700">Categories</legend>
        <div className="mt-2 flex flex-wrap gap-x-4 gap-y-2">
          {CATEGORIES.map((c) => (
            <label className="inline-flex items-center gap-2 text-sm text-gray-700" key={c}>
              <input
                type="checkbox"
                checked={p.interest_categories.includes(c)}
                onChange={(e: ChangeEvent<HTMLInputElement>) => {
                  const checked = e.currentTarget.checked;
                  setP({
                    ...p,
                    interest_categories: checked
                      ? [...p.interest_categories, c]
                      : p.interest_categories.filter((x) => x !== c),
                  });
                }}
                className="h-4 w-4 rounded border-gray-300 text-navy-900 focus:ring-navy-900"
              />
              {c}
            </label>
          ))}
        </div>
      </fieldset>
      <input
        className="input-field"
        placeholder="Countries (comma-separated ISO codes)"
        value={p.countries.join(", ")}
        onChange={(e: ChangeEvent<HTMLInputElement>) =>
          setP({
            ...p,
            countries: e.currentTarget.value
              .split(",")
              .map((s) => s.trim().toUpperCase())
              .filter(Boolean),
          })
        }
      />
      <button className="btn-primary" type="submit">
        Save preferences
      </button>
    </form>
  );
}

export default Flow;
