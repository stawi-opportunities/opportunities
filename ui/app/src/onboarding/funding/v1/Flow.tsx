// funding-onboarding-v1 — grant / funding matcher opt-in.
//
// Captures the org type the funder must accept, focus areas (used
// for vector / lexical matching against the call's eligibility),
// requested amount range, and geographic scope.

import { useState, type ChangeEvent } from "react";
import {
  LocationPicker,
  emptyLocationPreference,
  type LocationPreference,
} from "@/onboarding/shared/LocationPicker";
import { AmountRange } from "@/onboarding/shared/AmountRange";

export interface FundingPreferences {
  organisation_type: string;
  focus_areas: string[];
  geographic_scope: LocationPreference;
  funding_amount_needed_min: number;
  funding_amount_needed_max: number;
  currency: string;
}

const EMPTY: FundingPreferences = {
  organisation_type: "nonprofit",
  focus_areas: [],
  geographic_scope: emptyLocationPreference,
  funding_amount_needed_min: 0,
  funding_amount_needed_max: 0,
  currency: "USD",
};

export function Flow({
  initial,
  onSubmit,
}: {
  initial?: FundingPreferences;
  onSubmit: (p: FundingPreferences) => void;
}) {
  const [p, setP] = useState<FundingPreferences>(initial ?? EMPTY);
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit(p);
      }}
      className="space-y-6 max-w-xl"
    >
      <h2 className="text-2xl font-semibold text-gray-900">Tell us about your funding needs</h2>
      <label className="block text-sm">
        <span className="block font-medium text-gray-700">Organisation type</span>
        <select
          className="input-field mt-1"
          value={p.organisation_type}
          onChange={(e: ChangeEvent<HTMLSelectElement>) =>
            setP({ ...p, organisation_type: e.currentTarget.value })
          }
        >
          <option value="nonprofit">Nonprofit</option>
          <option value="for_profit">For-profit</option>
          <option value="individual">Individual</option>
        </select>
      </label>
      <input
        className="input-field"
        placeholder="Focus areas (comma-separated, e.g. Climate, Education)"
        value={p.focus_areas.join(", ")}
        onChange={(e: ChangeEvent<HTMLInputElement>) =>
          setP({
            ...p,
            focus_areas: e.currentTarget.value
              .split(",")
              .map((s) => s.trim())
              .filter(Boolean),
          })
        }
      />
      <AmountRange
        label="Funding amount needed"
        currency={p.currency}
        amountMin={p.funding_amount_needed_min}
        amountMax={p.funding_amount_needed_max}
        onAmountMin={(v) => setP({ ...p, funding_amount_needed_min: v })}
        onAmountMax={(v) => setP({ ...p, funding_amount_needed_max: v })}
        onCurrency={(c) => setP({ ...p, currency: c })}
      />
      <LocationPicker
        value={p.geographic_scope}
        onChange={(l) => setP({ ...p, geographic_scope: l })}
        label="Geographic scope"
      />
      <button className="btn-primary" type="submit">
        Save preferences
      </button>
    </form>
  );
}

export default Flow;
