// tender-onboarding-v1 — minimal opt-in for the tender matcher.
//
// Tenders match company entities, not individuals — so this collects
// company name, registration country, and a comma-separated list of
// capabilities the matcher uses to score notice descriptions.

import { useState, type ChangeEvent, type FormEvent } from "react";

export interface TenderPreferences {
  company_name: string;
  registration_country: string;
  capabilities: string[];
}

const EMPTY: TenderPreferences = {
  company_name: "",
  registration_country: "",
  capabilities: [],
};

export function Flow({
  initial,
  onSubmit,
}: {
  initial?: TenderPreferences;
  onSubmit: (p: TenderPreferences) => void;
}) {
  const [p, setP] = useState<TenderPreferences>(initial ?? EMPTY);
  const submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    onSubmit(p);
  };
  return (
    <form onSubmit={submit} className="space-y-4 max-w-xl">
      <h2 className="text-2xl font-semibold text-gray-900">Tell us about your company</h2>
      <input
        className="input-field"
        placeholder="Company name"
        value={p.company_name}
        onChange={(e: ChangeEvent<HTMLInputElement>) =>
          setP({ ...p, company_name: e.currentTarget.value })
        }
      />
      <input
        className="input-field"
        placeholder="Registration country (ISO code)"
        value={p.registration_country}
        maxLength={2}
        onChange={(e: ChangeEvent<HTMLInputElement>) =>
          setP({ ...p, registration_country: e.currentTarget.value.toUpperCase() })
        }
      />
      <input
        className="input-field"
        placeholder="Capabilities (comma-separated)"
        value={p.capabilities.join(", ")}
        onChange={(e: ChangeEvent<HTMLInputElement>) =>
          setP({
            ...p,
            capabilities: e.currentTarget.value
              .split(",")
              .map((s) => s.trim())
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
