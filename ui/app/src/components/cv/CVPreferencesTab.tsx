import { useCallback, useEffect, useState } from 'react';
import {
  fetchProfileFields,
  updateProfileFields,
  type ProfileFieldsPayload,
} from '@/api/profile';
import { PreferencesPanel } from '@/components/dashboard/PreferencesPanel';
import { Panel } from '@/components/dashboard/Panel';
import { Button } from '@/components/ui/Button';
import { useToast } from '@/hooks/useToast';
import { useMatchingProfileGate } from '@/hooks/useMatchingProfileGate';

/**
 * Matching preferences — every onboarding-critical field that drives match quality,
 * plus opportunity-kind filters.
 */
export function CVPreferencesTab() {
  const { push: toast } = useToast();
  const gate = useMatchingProfileGate({ enabled: true });
  const [pf, setPf] = useState<ProfileFieldsPayload>({});
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [dirty, setDirty] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const data = await fetchProfileFields();
      setPf(data ?? {});
      setDirty(false);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  function set<K extends keyof ProfileFieldsPayload>(key: K, value: ProfileFieldsPayload[K]) {
    setPf((prev) => ({ ...prev, [key]: value }));
    setDirty(true);
  }

  function setCSV(key: 'preferred_countries' | 'preferred_regions' | 'preferred_locations' | 'preferred_roles' | 'languages' | 'preferred_timezones', raw: string) {
    const arr = raw
      .split(/[,;]/)
      .map((s) => s.trim())
      .filter(Boolean);
    set(key, arr);
  }

  function csv(key: keyof ProfileFieldsPayload): string {
    const v = pf[key];
    if (Array.isArray(v)) return v.join(', ');
    return '';
  }

  async function save() {
    setSaving(true);
    try {
      await updateProfileFields({
        target_job_title: pf.target_job_title,
        current_title: pf.current_title,
        experience_level: pf.experience_level,
        seniority: pf.seniority,
        years_experience: pf.years_experience,
        job_search_status: pf.job_search_status,
        preferred_roles: pf.preferred_roles,
        preferred_countries: pf.preferred_countries,
        preferred_regions: pf.preferred_regions,
        preferred_locations: pf.preferred_locations,
        preferred_timezones: pf.preferred_timezones,
        remote_preference: pf.remote_preference,
        languages: pf.languages,
        salary_min: pf.salary_min,
        salary_max: pf.salary_max,
        currency: pf.currency,
        us_work_auth: pf.us_work_auth,
        needs_sponsorship: pf.needs_sponsorship,
      });
      setDirty(false);
      toast('Match preferences saved.', 'success');
    } catch (err) {
      toast(err instanceof Error ? err.message : 'Could not save preferences.', 'error');
    } finally {
      setSaving(false);
    }
  }

  if (loading) {
    return <p className="text-sm text-secondary">Loading preferences…</p>;
  }

  const missing = gate.readiness?.missing ?? [];

  return (
    <div className="space-y-6">
      <Panel title="Matching readiness">
        <p className="text-sm text-secondary">
          These fields power job matching. Keep them accurate so we only surface roles that fit.
        </p>
        {missing.length > 0 ? (
          <ul className="mt-3 list-disc space-y-1 pl-5 text-sm text-amber-800 dark:text-amber-200">
            {missing.map((m) => (
              <li key={m}>
                Missing: <code className="text-xs">{m}</code>
              </li>
            ))}
          </ul>
        ) : (
          <p className="mt-3 text-sm font-medium text-emerald-700 dark:text-emerald-400">
            Core matching signals look complete.
          </p>
        )}
        {gate.readiness && (
          <p className="mt-2 text-xs text-secondary">
            CV present: {gate.readiness.cvPresent ? 'yes' : 'no'} · Placement ready:{' '}
            {gate.readiness.placementReady ? 'yes' : 'no'}
          </p>
        )}
      </Panel>

      <Panel title="What you want">
        <div className="grid gap-3 sm:grid-cols-2">
          <TextField
            label="Target job title *"
            value={pf.target_job_title ?? ''}
            onChange={(v) => set('target_job_title', v)}
            placeholder="e.g. Product Manager"
          />
          <TextField
            label="Current title"
            value={pf.current_title ?? ''}
            onChange={(v) => set('current_title', v)}
          />
          <TextField
            label="Preferred roles (comma-separated)"
            value={csv('preferred_roles')}
            onChange={(v) => setCSV('preferred_roles', v)}
            placeholder="Backend, Platform, Fintech"
          />
          <SelectField
            label="Experience level *"
            value={pf.experience_level ?? pf.seniority ?? ''}
            onChange={(v) => {
              set('experience_level', v);
              set('seniority', v);
            }}
            options={[
              '',
              'intern',
              'junior',
              'mid',
              'senior',
              'lead',
              'manager',
              'director',
              'executive',
            ]}
          />
          <SelectField
            label="Job search status"
            value={pf.job_search_status ?? ''}
            onChange={(v) => set('job_search_status', v)}
            options={['', 'actively_looking', 'open_to_offers', 'not_looking', 'employed_open']}
          />
          <TextField
            label="Years of experience"
            value={pf.years_experience != null ? String(pf.years_experience) : ''}
            onChange={(v) => set('years_experience', v ? Number(v) || 0 : 0)}
            placeholder="5"
          />
        </div>
      </Panel>

      <Panel title="Location & work setup">
        <div className="grid gap-3 sm:grid-cols-2">
          <TextField
            label="Preferred countries *"
            value={csv('preferred_countries')}
            onChange={(v) => setCSV('preferred_countries', v)}
            placeholder="KE, NG, Remote"
          />
          <TextField
            label="Preferred regions"
            value={csv('preferred_regions')}
            onChange={(v) => setCSV('preferred_regions', v)}
            placeholder="East Africa, EU"
          />
          <TextField
            label="Preferred cities / locations"
            value={csv('preferred_locations')}
            onChange={(v) => setCSV('preferred_locations', v)}
          />
          <TextField
            label="Preferred timezones"
            value={csv('preferred_timezones')}
            onChange={(v) => setCSV('preferred_timezones', v)}
            placeholder="EAT, CET"
          />
          <SelectField
            label="Remote preference"
            value={pf.remote_preference ?? ''}
            onChange={(v) => set('remote_preference', v)}
            options={['', 'remote', 'hybrid', 'onsite', 'flexible']}
          />
          <TextField
            label="Languages"
            value={csv('languages')}
            onChange={(v) => setCSV('languages', v)}
            placeholder="English, Swahili, French"
          />
        </div>
        <div className="mt-3 flex flex-wrap gap-4 text-sm text-main">
          <label className="inline-flex items-center gap-2">
            <input
              type="checkbox"
              checked={pf.us_work_auth === true}
              onChange={(e) => set('us_work_auth', e.target.checked ? true : false)}
            />
            Authorized to work in the US
          </label>
          <label className="inline-flex items-center gap-2">
            <input
              type="checkbox"
              checked={pf.needs_sponsorship === true}
              onChange={(e) => set('needs_sponsorship', e.target.checked ? true : false)}
            />
            Needs visa sponsorship
          </label>
        </div>
      </Panel>

      <Panel title="Compensation">
        <div className="grid gap-3 sm:grid-cols-3">
          <TextField
            label="Salary min"
            value={pf.salary_min != null && pf.salary_min > 0 ? String(pf.salary_min) : ''}
            onChange={(v) => set('salary_min', v ? Number(v) : 0)}
            placeholder="50000"
          />
          <TextField
            label="Salary max"
            value={pf.salary_max != null && pf.salary_max > 0 ? String(pf.salary_max) : ''}
            onChange={(v) => set('salary_max', v ? Number(v) : 0)}
            placeholder="90000"
          />
          <TextField
            label="Currency"
            value={pf.currency ?? ''}
            onChange={(v) => set('currency', v)}
            placeholder="USD"
          />
        </div>
        <p className="mt-2 text-xs text-secondary">
          Used for match filtering — not shown to employers without your action.
        </p>
      </Panel>

      <div className="flex flex-wrap items-center gap-2">
        <Button type="button" variant="primary" disabled={saving || !dirty} onClick={() => void save()}>
          {saving ? 'Saving…' : dirty ? 'Save match preferences' : 'Saved'}
        </Button>
      </div>

      <div>
        <h3 className="mb-2 text-base font-semibold text-main">Opportunity kinds & filters</h3>
        <p className="mb-3 text-sm text-secondary">
          Opt into kinds you want matched (jobs, scholarships, etc.) and refine filters per kind.
        </p>
        <PreferencesPanel />
      </div>
    </div>
  );
}

function TextField({
  label,
  value,
  onChange,
  placeholder,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
}) {
  return (
    <label className="block text-sm">
      <span className="font-medium text-main">{label}</span>
      <input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className="mt-1 w-full rounded-md border border-muted bg-surface px-3 py-2 text-sm text-main"
      />
    </label>
  );
}

function SelectField({
  label,
  value,
  onChange,
  options,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  options: string[];
}) {
  return (
    <label className="block text-sm">
      <span className="font-medium text-main">{label}</span>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="mt-1 w-full rounded-md border border-muted bg-surface px-3 py-2 text-sm text-main"
      >
        {options.map((o) => (
          <option key={o || '_'} value={o}>
            {o || '— select —'}
          </option>
        ))}
      </select>
    </label>
  );
}
