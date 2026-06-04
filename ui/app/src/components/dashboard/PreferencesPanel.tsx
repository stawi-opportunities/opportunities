import { useEffect, useState } from 'react';
import { authRuntime } from '@/auth/runtime';
import { OnboardingRouter } from '@/onboarding/router';
import { Panel } from './Panel';

// Per-kind onboarding tabs — each entry maps a kind id to the flow id
// the OnboardingRouter dispatches to. Matches the registry kinds
// (job, scholarship, tender, deal, funding) wired in Phase 1.3.
const PREFERENCE_KINDS: ReadonlyArray<{ kind: string; flow: string; label: string }> = [
  { kind: 'job', flow: 'job-onboarding-v1', label: 'Jobs' },
  { kind: 'scholarship', flow: 'scholarship-onboarding-v1', label: 'Scholarships' },
  { kind: 'tender', flow: 'tender-onboarding-v1', label: 'Tenders' },
  { kind: 'deal', flow: 'deal-onboarding-v1', label: 'Deals' },
  { kind: 'funding', flow: 'funding-onboarding-v1', label: 'Funding' },
];

export function PreferencesPanel() {
  const [active, setActive] = useState<string>(PREFERENCE_KINDS[0]!.kind);
  const [status, setStatus] = useState<'idle' | 'saving' | 'saved' | 'error'>('idle');
  const [errMsg, setErrMsg] = useState<string | null>(null);
  // Enabled kinds come from /candidates/match-kinds; the matching service
  // returns only kinds whose matcher isn't a stub. While loading we render
  // nothing rather than show tabs that may disappear once the response
  // lands. On fetch failure we fall back to the production-ready pair so
  // the dashboard stays usable.
  const [enabledKinds, setEnabledKinds] = useState<string[] | null>(null);

  useEffect(() => {
    let cancelled = false;
    authRuntime()
      .fetch<{ enabled_kinds?: string[] }>('/candidates/match-kinds')
      .then((data) => {
        if (cancelled) return;
        setEnabledKinds(data.enabled_kinds ?? ['job', 'scholarship']);
      })
      .catch(() => {
        if (cancelled) return;
        setEnabledKinds(['job', 'scholarship']);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const visibleKinds =
    enabledKinds === null
      ? PREFERENCE_KINDS
      : PREFERENCE_KINDS.filter((k) => enabledKinds.includes(k.kind));

  // If the active tab got filtered out (e.g. flag flip while mounted),
  // snap back to the first visible kind.
  useEffect(() => {
    if (visibleKinds.length === 0) return;
    if (!visibleKinds.some((k) => k.kind === active)) {
      setActive(visibleKinds[0]!.kind);
    }
  }, [visibleKinds, active]);

  const activeEntry =
    visibleKinds.find((k) => k.kind === active) ?? visibleKinds[0] ?? PREFERENCE_KINDS[0]!;

  async function persist(kind: string, prefs: unknown) {
    setStatus('saving');
    setErrMsg(null);
    try {
      // Posts the polymorphic PreferencesUpdatedV1 envelope (Phase 7.6)
      // with just the active kind populated. The matching service merges
      // server-side; we only carry the slice the user just edited.
      await authRuntime().fetch('/candidates/preferences', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ opt_ins: { [kind]: prefs } }),
      });
      setStatus('saved');
    } catch (e) {
      setStatus('error');
      setErrMsg(e instanceof Error ? e.message : "Couldn't save preferences");
    }
  }

  return (
    <Panel title="Match preferences">
      <p className="text-sm text-gray-600">
        Opt into the kinds of opportunities you want matched. We'll only run matchers for kinds
        you've configured.
      </p>
      <nav
        className="mt-4 flex flex-wrap gap-1 border-b border-gray-200"
        role="tablist"
        aria-label="Opportunity kinds"
      >
        {visibleKinds.map(({ kind, label }) => {
          const on = active === kind;
          return (
            <button
              key={kind}
              type="button"
              role="tab"
              aria-selected={on}
              className={`px-4 py-2 text-sm font-medium transition-colors ${
                on
                  ? 'border-b-2 border-accent-500 text-navy-900'
                  : 'border-b-2 border-transparent text-gray-600 hover:text-gray-900'
              }`}
              onClick={() => {
                setActive(kind);
                setStatus('idle');
                setErrMsg(null);
              }}
            >
              {label}
            </button>
          );
        })}
      </nav>
      <div className="mt-6">
        <OnboardingRouter
          flowId={activeEntry.flow}
          onSubmit={(prefs) => void persist(activeEntry.kind, prefs)}
        />
      </div>
      {status === 'saving' && <p className="mt-3 text-sm text-gray-500">Saving…</p>}
      {status === 'saved' && <p className="mt-3 text-sm text-emerald-700">Preferences saved.</p>}
      {status === 'error' && (
        <p className="mt-3 text-sm text-red-700" role="alert">
          {errMsg ?? "Couldn't save preferences."}
        </p>
      )}
    </Panel>
  );
}
