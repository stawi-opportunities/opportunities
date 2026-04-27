// scholarship-onboarding-v1 — opt-in form for the scholarship matcher.
//
// Posted under opt_ins["scholarship"]. Mirrors fields the scholarship
// matcher in apps/matching/business/match/scholarship checks: degree
// level, fields of study, applicant nationality (eligibility filter),
// minimum GPA, target country, and how soon the deadline must fall.

import { useState, type ChangeEvent } from "react";
import {
  LocationPicker,
  emptyLocationPreference,
  type LocationPreference,
} from "@/onboarding/shared/LocationPicker";

export interface ScholarshipPreferences {
  degree_levels: string[];
  fields_of_study: string[];
  nationality: string;
  gpa_min: number;
  locations: LocationPreference;
  deadline_within_days: number;
}

const EMPTY: ScholarshipPreferences = {
  degree_levels: [],
  fields_of_study: [],
  nationality: "",
  gpa_min: 0,
  locations: emptyLocationPreference,
  deadline_within_days: 90,
};

const DEGREE_LEVELS = ["undergraduate", "masters", "doctoral", "postdoc"];

export function Flow({
  initial,
  onSubmit,
}: {
  initial?: ScholarshipPreferences;
  onSubmit: (p: ScholarshipPreferences) => void;
}) {
  const [p, setP] = useState<ScholarshipPreferences>(initial ?? EMPTY);
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit(p);
      }}
      className="space-y-6 max-w-xl"
    >
      <h2 className="text-2xl font-semibold text-gray-900">Find scholarships that fit you</h2>
      <fieldset>
        <legend className="text-sm font-medium text-gray-700">Degree level</legend>
        <div className="mt-2 flex flex-wrap gap-x-4 gap-y-2">
          {DEGREE_LEVELS.map((d) => (
            <label className="inline-flex items-center gap-2 text-sm text-gray-700" key={d}>
              <input
                type="checkbox"
                checked={p.degree_levels.includes(d)}
                onChange={(e: ChangeEvent<HTMLInputElement>) => {
                  const checked = e.currentTarget.checked;
                  setP({
                    ...p,
                    degree_levels: checked
                      ? [...p.degree_levels, d]
                      : p.degree_levels.filter((x) => x !== d),
                  });
                }}
                className="h-4 w-4 rounded border-gray-300 text-navy-900 focus:ring-navy-900"
              />
              {d}
            </label>
          ))}
        </div>
      </fieldset>
      <input
        type="text"
        placeholder="Fields of study (comma-separated)"
        value={p.fields_of_study.join(", ")}
        onChange={(e: ChangeEvent<HTMLInputElement>) =>
          setP({
            ...p,
            fields_of_study: e.currentTarget.value
              .split(",")
              .map((s) => s.trim())
              .filter(Boolean),
          })
        }
        className="input-field"
      />
      <input
        type="text"
        placeholder="Your nationality (ISO code, e.g. KE)"
        value={p.nationality}
        onChange={(e: ChangeEvent<HTMLInputElement>) =>
          setP({ ...p, nationality: e.currentTarget.value.toUpperCase() })
        }
        className="input-field"
        maxLength={2}
      />
      <input
        type="number"
        placeholder="Minimum GPA"
        value={p.gpa_min || ""}
        onChange={(e: ChangeEvent<HTMLInputElement>) =>
          setP({ ...p, gpa_min: Number(e.currentTarget.value) })
        }
        className="input-field"
        step="0.1"
        min="0"
        max="4"
      />
      <LocationPicker
        value={p.locations}
        onChange={(l) => setP({ ...p, locations: l })}
        label="Where do you want to study?"
      />
      <label className="block text-sm">
        <span className="block font-medium text-gray-700">
          Only show scholarships closing in the next…
        </span>
        <input
          type="number"
          value={p.deadline_within_days || ""}
          onChange={(e: ChangeEvent<HTMLInputElement>) =>
            setP({ ...p, deadline_within_days: Number(e.currentTarget.value) })
          }
          className="input-field mt-1 w-32"
          min="1"
          max="365"
        />
        <span className="ml-2 text-gray-600">days</span>
      </label>
      <button type="submit" className="btn-primary">
        Save preferences
      </button>
    </form>
  );
}

export default Flow;
