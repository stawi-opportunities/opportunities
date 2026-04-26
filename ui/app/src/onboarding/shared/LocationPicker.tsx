// LocationPicker — shared geographic preferences block reused across
// kind-specific onboarding flows (job, scholarship, funding). The
// surface is intentionally simple: comma-separated free-text inputs
// for ISO-3166 alpha-2 country codes + cities, plus a remote toggle.
// Coordinates / radius live in the type but are not yet edited here —
// the registry data already supports them, future revisions will add
// a map picker.

import type { ChangeEvent } from "react";

export interface LocationPreference {
  countries: string[];
  regions: string[];
  cities: string[];
  near_lat?: number;
  near_lon?: number;
  radius_km?: number;
  remote_ok: boolean;
}

export const emptyLocationPreference: LocationPreference = {
  countries: [],
  regions: [],
  cities: [],
  remote_ok: false,
};

export function LocationPicker({
  value,
  onChange,
  label = "Locations",
}: {
  value: LocationPreference;
  onChange: (next: LocationPreference) => void;
  label?: string;
}) {
  return (
    <div className="space-y-3">
      <label className="block text-sm font-medium text-gray-700">{label}</label>
      <input
        type="text"
        placeholder="Countries (comma-separated ISO codes, e.g. KE, TZ, UG)"
        value={value.countries.join(", ")}
        onChange={(e: ChangeEvent<HTMLInputElement>) =>
          onChange({
            ...value,
            countries: e.currentTarget.value
              .split(",")
              .map((s) => s.trim().toUpperCase())
              .filter(Boolean),
          })
        }
        className="input-field"
      />
      <input
        type="text"
        placeholder="Cities (comma-separated)"
        value={value.cities.join(", ")}
        onChange={(e: ChangeEvent<HTMLInputElement>) =>
          onChange({
            ...value,
            cities: e.currentTarget.value
              .split(",")
              .map((s) => s.trim())
              .filter(Boolean),
          })
        }
        className="input-field"
      />
      <label className="flex items-center gap-2 text-sm text-gray-700">
        <input
          type="checkbox"
          checked={value.remote_ok}
          onChange={(e: ChangeEvent<HTMLInputElement>) =>
            onChange({ ...value, remote_ok: e.currentTarget.checked })
          }
          className="h-4 w-4 rounded border-gray-300 text-navy-900 focus:ring-navy-900"
        />
        Open to remote
      </label>
    </div>
  );
}
