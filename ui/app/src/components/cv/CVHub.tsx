import { useState } from 'react';
import { CVDetailsTab } from './CVDetailsTab';
import { CVPreferencesTab } from './CVPreferencesTab';
import { CVExportTab } from './CVExportTab';

type CVTab = 'details' | 'preferences' | 'export';

const TABS: { id: CVTab; label: string; hint: string }[] = [
  { id: 'details', label: 'Details', hint: 'Edit CV, upload, ATS improve' },
  { id: 'preferences', label: 'Preferences', hint: 'Matching criteria' },
  { id: 'export', label: 'Export', hint: 'HTML / PDF templates' },
];

/**
 * CV hub — Details (structured CV + ATS assist + upload), Preferences, Export.
 * ATS score / rewrites live on Details (not a separate tab).
 */
export function CVHub() {
  const [tab, setTab] = useState<CVTab>('details');

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-lg font-semibold text-main">Your CV</h2>
        <p className="mt-1 text-sm text-secondary">
          Maintain a living profile for matching: edit sections like LinkedIn, order a $2 ATS report
          scored against your matched jobs (emailed to you), set match preferences, and export
          polished applications.
        </p>
      </div>

      <nav
        className="-mb-px flex flex-wrap gap-x-6 gap-y-2 border-b border-muted"
        aria-label="CV sections"
      >
        {TABS.map((t) => (
          <button
            key={t.id}
            type="button"
            onClick={() => setTab(t.id)}
            className={`border-b-2 px-1 pb-3 text-sm font-medium transition-colors ${
              tab === t.id
                ? 'border-accent-600 text-accent-700'
                : 'border-transparent text-secondary hover:border-muted hover:text-main'
            }`}
            title={t.hint}
          >
            {t.label}
          </button>
        ))}
      </nav>

      {tab === 'details' && <CVDetailsTab />}
      {tab === 'preferences' && <CVPreferencesTab />}
      {tab === 'export' && <CVExportTab />}
    </div>
  );
}
