import type { ReactNode } from 'react';
import { useCallback, useEffect, useRef, useState } from 'react';
import {
  fetchMeCV,
  fetchProfileFields,
  updateProfileFields,
  uploadCV,
  type MeCVDocument,
} from '@/api/profile';
import { purchaseATSReport } from '@/api/tools';
import { useCandidateProfile } from '@/hooks/useCandidateProfile';
import { useToast } from '@/hooks/useToast';
import { Button } from '@/components/ui/Button';
import { Spinner } from '@/components/ui/Spinner';
import { Panel } from '@/components/dashboard/Panel';
import { usePreferenceChatOptional } from '@/components/preference-chat';
import {
  emptyEducation,
  emptyExperience,
  hydrateStructuredCV,
  structuredCVToPlainText,
  structuredCVToProfileFields,
  type CVEducation,
  type CVExperience,
  type StructuredCV,
} from '@/utils/structuredCV';

/**
 * Details tab: LinkedIn-style structured editor, upload at bottom,
 * paid $2 ATS report (emailed vs matched jobs), optional chat assist.
 */
export function CVDetailsTab() {
  const { push: toast } = useToast();
  const profileQ = useCandidateProfile();
  const preferenceChat = usePreferenceChatOptional();
  const fileRef = useRef<HTMLInputElement>(null);

  const [doc, setDoc] = useState<StructuredCV>(() => hydrateStructuredCV(null));
  const [cvMeta, setCvMeta] = useState<MeCVDocument | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [uploadPhase, setUploadPhase] = useState<'idle' | 'uploading' | 'reading' | 'done'>('idle');
  const [lastFilled, setLastFilled] = useState<string[]>([]);
  const [dirty, setDirty] = useState(false);
  const [buyingReport, setBuyingReport] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [pf, meCv] = await Promise.all([fetchProfileFields(), fetchMeCV()]);
      setCvMeta(meCv);
      // Contacts are only contact_ids server-side; UI contact fields start empty
      // unless the user edits them this session (CreateContact on save).
      const hydrated = hydrateStructuredCV(pf, { name: pf?.name });
      if (!hydrated.basics.headline && profileQ.data?.current_title) {
        hydrated.basics.headline = profileQ.data.current_title;
      }
      setDoc(hydrated);
      setDirty(false);
    } finally {
      setLoading(false);
    }
  }, [profileQ.data?.current_title]);

  useEffect(() => {
    void load();
  }, [load]);

  function patch(next: StructuredCV) {
    setDoc({ ...next, source: 'manual', updated_at: new Date().toISOString() });
    setDirty(true);
  }

  async function saveDocument() {
    setSaving(true);
    try {
      const body = structuredCVToProfileFields(doc);
      await updateProfileFields(body);
      setDirty(false);
      toast('CV saved. Matching will use your updates shortly.', 'success');
    } catch (err) {
      toast(err instanceof Error ? err.message : 'Could not save CV.', 'error');
    } finally {
      setSaving(false);
    }
  }

  async function onUpload(file: File) {
    setUploading(true);
    setUploadPhase('uploading');
    setLastFilled([]);
    try {
      setUploadPhase('reading');
      const res = await uploadCV(file);
      const filled = res.filled_fields ?? [];
      setLastFilled(filled);

      // Prefer server merge for immediate hydration; fall back to full reload.
      if (res.profile_fields) {
        const contactDetails = (res.platform_contacts ?? [])
          .map((c) => c.detail)
          .filter(Boolean) as string[];
        const hydrated = hydrateStructuredCV(res.profile_fields, {
          name: res.profile_fields.name,
          contact_details: contactDetails,
        });
        hydrated.source = 'upload';
        hydrated.updated_at = new Date().toISOString();
        setDoc(hydrated);
        setCvMeta({
          ok: true,
          present: true,
          cv_version: res.cv_version,
          file_id: res.file_id,
          content_uri: res.content_uri,
          content_hash: res.content_hash,
          cv_length: res.cv_length,
          extracted_text: res.extracted_text,
          placement_ready: res.placement_ready,
        });
        setDirty(false);
      } else {
        await load();
        setDirty(false);
      }

      if (!res.fully_processed) {
        setUploadPhase('idle');
        toast(
          'CV was received but AI sectioning did not finish. Please try again — nothing was left half-done for you to fix manually.',
          'error'
        );
        return;
      }

      setUploadPhase('done');
      if (filled.length > 0) {
        const labels = filled
          .slice(0, 6)
          .map((k) => k.replace(/_/g, ' '))
          .join(', ');
        const more = filled.length > 6 ? ` (+${filled.length - 6} more)` : '';
        toast(
          `CV fully processed — filled ${filled.length} missing field${filled.length === 1 ? '' : 's'}: ${labels}${more}. Already saved for matching.`,
          'success'
        );
      } else {
        toast(
          'CV fully processed. Your profile already had the main details — nothing empty to overwrite.',
          'success'
        );
      }
    } catch (err) {
      setUploadPhase('idle');
      const msg = err instanceof Error ? err.message : 'Could not upload CV.';
      // Surface server problem codes helpfully.
      if (/text_extraction|empty_cv|unsupported/i.test(msg)) {
        toast(
          'We could not read that file. Try a text-based PDF, DOCX, or TXT (not a scanned image).',
          'error'
        );
      } else if (/structure_unavailable|structure_failed/i.test(msg)) {
        toast(
          'AI could not finish sectioning your CV. Please retry — we do not leave a half-imported profile for you to complete by hand.',
          'error'
        );
      } else if (/store_failed|502|503|504/i.test(msg)) {
        toast(
          'Upload hit a storage issue. Please retry in a moment — if it persists, contact support.',
          'error'
        );
      } else if (/not authenticated|TOKEN|401/i.test(msg)) {
        toast('Session expired — sign in again, then re-upload your CV.', 'error');
      } else {
        toast(msg || 'Could not upload CV.', 'error');
      }
    } finally {
      setUploading(false);
    }
  }

  async function buyATSReport() {
    if (dirty) {
      toast('Save your CV first so the report uses the latest version.', 'info');
      return;
    }
    setBuyingReport(true);
    try {
      const res = await purchaseATSReport();
      if (res.redirect_url) {
        toast('Redirecting to secure checkout ($2)…', 'success');
        window.location.href = res.redirect_url;
        return;
      }
      toast(
        res.message || 'Checkout started — complete payment to receive your report by email.',
        'info'
      );
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      if (/cv_required/i.test(msg)) {
        toast('Upload or complete your CV before purchasing a report.', 'error');
      } else {
        toast(msg || 'Could not start ATS report checkout.', 'error');
      }
    } finally {
      setBuyingReport(false);
    }
  }

  const present = Boolean(
    cvMeta?.present || cvMeta?.extracted_text || structuredCVToPlainText(doc)
  );

  if (loading) {
    return <p className="text-sm text-secondary">Loading CV…</p>;
  }

  const uploadStatusLabel =
    uploadPhase === 'uploading'
      ? 'Uploading file…'
      : uploadPhase === 'reading'
        ? 'AI is sectioning your CV…'
        : uploading
          ? 'Working…'
          : present
            ? 'Replace CV file'
            : 'Upload CV — AI fills empty sections';

  return (
    <div className="relative space-y-6">
      {uploading && (
        <div
          className="fixed inset-0 z-40 flex items-center justify-center bg-black/40 backdrop-blur-[1px]"
          role="status"
          aria-live="polite"
          aria-busy="true"
        >
          <div className="mx-4 flex max-w-sm flex-col items-center gap-3 rounded-xl border border-muted bg-surface px-6 py-5 shadow-xl">
            <Spinner size={36} />
            <p className="text-center text-sm font-medium text-main">{uploadStatusLabel}</p>
            <p className="text-center text-xs text-secondary">
              Extracting full text, contacts, experience, skills, and certifications. Stay on this
              page — processing finishes before we mark the upload done.
            </p>
          </div>
        </div>
      )}

      {/* Primary CTA: upload first */}
      <Panel title="Start with your CV" id="cv-upload">
        <p className="text-sm text-secondary">
          Drop a PDF, Word, or text CV. Processing is{' '}
          <span className="font-medium text-main">fully synchronous</span>: we extract the complete
          text, run AI sectioning (name, all contacts, about, full experience bullets, skills,
          certifications), fill empty fields, and save before this finishes. Existing saved values
          are never overwritten.
        </p>
        {present && (
          <p className="mt-2 text-sm">
            <span className="font-medium text-emerald-700 dark:text-emerald-400">CV on file</span>
            {cvMeta?.cv_version != null && (
              <span className="text-secondary"> · version {cvMeta.cv_version}</span>
            )}
            {cvMeta?.cv_length != null && cvMeta.cv_length > 0 && (
              <span className="text-secondary"> · {cvMeta.cv_length.toLocaleString()} chars</span>
            )}
          </p>
        )}
        {lastFilled.length > 0 && (
          <div className="mt-3 rounded-lg border border-emerald-200 bg-emerald-50 px-3 py-2 text-sm text-emerald-900 dark:border-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-100">
            Auto-filled from last upload:{' '}
            <span className="font-medium">
              {lastFilled.map((k) => k.replace(/_/g, ' ')).join(', ')}
            </span>
          </div>
        )}
        <div className="mt-4 flex flex-wrap items-center gap-3">
          <input
            ref={fileRef}
            type="file"
            accept=".pdf,.doc,.docx,.txt,.rtf,application/pdf,application/msword,application/vnd.openxmlformats-officedocument.wordprocessingml.document,text/plain"
            className="hidden"
            onChange={(e) => {
              const f = e.target.files?.[0];
              if (f) void onUpload(f);
              e.target.value = '';
            }}
          />
          <Button
            type="button"
            variant="primary"
            size="sm"
            disabled={uploading}
            onClick={() => fileRef.current?.click()}
          >
            {uploading ? (
              <span className="inline-flex items-center gap-2">
                <Spinner size={16} className="text-white" />
                {uploadStatusLabel}
              </span>
            ) : (
              uploadStatusLabel
            )}
          </Button>
          {uploading && (
            <span className="inline-flex items-center gap-2 text-xs text-secondary">
              <Spinner size={14} />
              Processing — please wait…
            </span>
          )}
        </div>
        <p className="mt-3 text-xs text-secondary">
          Tip: text-based PDFs and DOCX work best. Scanned image-only PDFs may not extract cleanly.
        </p>
      </Panel>

      {/* Header / basics */}
      <Panel title="Profile header">
        <div className="grid gap-3 sm:grid-cols-2">
          <Field
            label="Full name"
            value={doc.basics.name}
            onChange={(v) => patch({ ...doc, basics: { ...doc.basics, name: v } })}
            placeholder="Your name"
          />
          <Field
            label="Headline"
            value={doc.basics.headline}
            onChange={(v) => patch({ ...doc, basics: { ...doc.basics, headline: v } })}
            placeholder="e.g. Senior Backend Engineer"
          />
          <Field
            label="Location"
            value={doc.basics.location ?? ''}
            onChange={(v) => patch({ ...doc, basics: { ...doc.basics, location: v } })}
            placeholder="City, country"
          />
        </div>
        <div className="mt-3 space-y-3">
          <ChipEditor
            label="Phone numbers"
            values={
              doc.basics.phones?.length
                ? doc.basics.phones
                : doc.basics.phone
                  ? [doc.basics.phone]
                  : []
            }
            onChange={(phones) =>
              patch({
                ...doc,
                basics: {
                  ...doc.basics,
                  phones,
                  phone: phones[0] || '',
                },
              })
            }
            placeholder="+254… then Enter"
          />
          <ChipEditor
            label="Emails (from CV)"
            values={
              doc.basics.emails?.length
                ? doc.basics.emails
                : doc.basics.email
                  ? [doc.basics.email]
                  : []
            }
            onChange={(emails) =>
              patch({
                ...doc,
                basics: {
                  ...doc.basics,
                  emails,
                  email: emails[0] || '',
                },
              })
            }
            placeholder="you@example.com then Enter"
          />
        </div>
      </Panel>

      <Panel title="About">
        <textarea
          value={doc.summary}
          onChange={(e) => patch({ ...doc, summary: e.target.value })}
          rows={10}
          placeholder="Full professional summary from your CV — paste or edit the complete About / Profile section."
          className="min-h-[12rem] w-full rounded-md border border-muted bg-surface px-3 py-2 text-sm text-main"
        />
      </Panel>

      <SectionList
        title="Experience"
        emptyLabel="Add a role"
        onAdd={() => patch({ ...doc, experience: [emptyExperience(), ...doc.experience] })}
      >
        {doc.experience.map((e, idx) => (
          <ExperienceCard
            key={e.id}
            entry={e}
            onChange={(next) => {
              const experience = [...doc.experience];
              experience[idx] = next;
              patch({ ...doc, experience });
            }}
            onRemove={() =>
              patch({ ...doc, experience: doc.experience.filter((x) => x.id !== e.id) })
            }
          />
        ))}
      </SectionList>

      <SectionList
        title="Education"
        emptyLabel="Add education"
        onAdd={() => patch({ ...doc, education: [...doc.education, emptyEducation()] })}
      >
        {doc.education.map((ed, idx) => (
          <EducationCard
            key={ed.id}
            entry={ed}
            onChange={(next) => {
              const education = [...doc.education];
              education[idx] = next;
              patch({ ...doc, education });
            }}
            onRemove={() =>
              patch({ ...doc, education: doc.education.filter((x) => x.id !== ed.id) })
            }
          />
        ))}
      </SectionList>

      <Panel title="Skills">
        <ChipEditor
          label="Strong skills"
          values={doc.skills.strong}
          onChange={(strong) => patch({ ...doc, skills: { ...doc.skills, strong } })}
        />
        <div className="mt-3">
          <ChipEditor
            label="Working knowledge"
            values={doc.skills.working}
            onChange={(working) => patch({ ...doc, skills: { ...doc.skills, working } })}
          />
        </div>
        <div className="mt-3">
          <ChipEditor
            label="Tools & frameworks"
            values={doc.skills.tools}
            onChange={(tools) => patch({ ...doc, skills: { ...doc.skills, tools } })}
          />
        </div>
      </Panel>

      <Panel title="Certifications & languages">
        <ChipEditor
          label="Certifications"
          values={doc.certifications}
          onChange={(certifications) => patch({ ...doc, certifications })}
        />
        <div className="mt-3">
          <ChipEditor
            label="Languages"
            values={doc.languages}
            onChange={(languages) => patch({ ...doc, languages })}
          />
        </div>
      </Panel>

      <div className="flex flex-wrap items-center gap-2">
        <Button
          type="button"
          variant="primary"
          disabled={saving || !dirty}
          onClick={() => void saveDocument()}
        >
          {saving ? 'Saving…' : dirty ? 'Save CV' : 'Saved'}
        </Button>
        {dirty && (
          <span className="text-xs text-amber-700 dark:text-amber-300">Unsaved changes</span>
        )}
      </div>

      {/* Paid ATS report + optional chat assist */}
      <div className="grid gap-6 lg:grid-cols-5">
        <div className="lg:col-span-3">
          <Panel title="ATS report">
            <p className="text-sm text-secondary">
              Get a comprehensive ATS score of your CV against the jobs we already matched to your
              preferences. After a secure <strong>$2</strong> payment, we email the full report
              (HTML attachment) to your account email — overall score, priority fixes, rewrites, and
              per-job fit.
            </p>
            <div className="mt-4 flex flex-wrap items-center gap-3">
              <Button
                type="button"
                variant="primary"
                disabled={buyingReport || !present}
                onClick={() => void buyATSReport()}
              >
                {buyingReport ? 'Starting checkout…' : 'Get ATS report · $2'}
              </Button>
              {!present && (
                <span className="text-xs text-amber-700 dark:text-amber-300">
                  Add CV content first
                </span>
              )}
            </div>
            <p className="mt-3 text-xs text-secondary">
              Save your latest edits before purchasing. Delivery is by email after payment succeeds
              (usually within a few minutes).
            </p>
          </Panel>
        </div>

        <div className="lg:col-span-2">
          <Panel title="Improve with chat">
            <p className="text-sm text-secondary">
              Prefer to iterate in conversation? Open the assistant to refine bullets, then save
              your CV.
            </p>
            {preferenceChat ? (
              <button
                type="button"
                onClick={() => preferenceChat.openRefine()}
                className="mt-3 inline-flex items-center rounded-lg bg-navy-900 px-3.5 py-2 text-sm font-medium text-white shadow-sm hover:bg-navy-800 dark:bg-accent-600 dark:hover:bg-accent-500"
              >
                Open CV assistant
              </button>
            ) : (
              <p className="mt-3 text-xs text-secondary">
                Chat assistant is unavailable on this page.
              </p>
            )}
          </Panel>
        </div>
      </div>

      <p className="text-xs text-secondary">
        After upload, empty sections are already saved for matching. Use{' '}
        <span className="font-medium text-main">Save CV</span> only for extra manual tweaks.
        Re-uploading only fills remaining empty fields.
      </p>
    </div>
  );
}

function Field({
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

function SectionList({
  title,
  children,
  onAdd,
  emptyLabel,
}: {
  title: string;
  children: ReactNode;
  onAdd: () => void;
  emptyLabel: string;
}) {
  return (
    <Panel title={title}>
      <div className="space-y-3">{children}</div>
      <Button type="button" size="sm" variant="secondary" className="mt-3" onClick={onAdd}>
        + {emptyLabel}
      </Button>
    </Panel>
  );
}

function ExperienceCard({
  entry,
  onChange,
  onRemove,
}: {
  entry: CVExperience;
  onChange: (e: CVExperience) => void;
  onRemove: () => void;
}) {
  return (
    <div className="rounded-lg border border-muted bg-surface-muted p-3 sm:p-4">
      <div className="grid gap-2 sm:grid-cols-2">
        <Field
          label="Title"
          value={entry.title}
          onChange={(title) => onChange({ ...entry, title })}
        />
        <Field
          label="Company"
          value={entry.company}
          onChange={(company) => onChange({ ...entry, company })}
        />
        <Field
          label="Start"
          value={entry.start}
          onChange={(start) => onChange({ ...entry, start })}
          placeholder="2021-01"
        />
        <Field
          label="End"
          value={entry.current ? 'Present' : (entry.end ?? '')}
          onChange={(end) => onChange({ ...entry, end, current: false })}
          placeholder="2024-06 or leave blank"
        />
      </div>
      <label className="mt-2 flex items-center gap-2 text-sm text-main">
        <input
          type="checkbox"
          checked={Boolean(entry.current)}
          onChange={(e) =>
            onChange({
              ...entry,
              current: e.target.checked,
              end: e.target.checked ? '' : entry.end,
            })
          }
        />
        I currently work here
      </label>
      <label className="mt-2 block text-sm">
        <span className="font-medium text-main">Description</span>
        <textarea
          value={entry.description}
          onChange={(e) => onChange({ ...entry, description: e.target.value })}
          rows={8}
          placeholder="Full role description — keep every bullet, outcome, metric, and stack from your CV."
          className="mt-1 min-h-[10rem] w-full rounded-md border border-muted bg-surface px-3 py-2 text-sm text-main"
        />
      </label>
      <div className="mt-2 flex justify-end">
        <Button type="button" size="sm" variant="ghost" onClick={onRemove}>
          Remove
        </Button>
      </div>
    </div>
  );
}

function EducationCard({
  entry,
  onChange,
  onRemove,
}: {
  entry: CVEducation;
  onChange: (e: CVEducation) => void;
  onRemove: () => void;
}) {
  return (
    <div className="rounded-lg border border-muted bg-surface-muted p-3 sm:p-4">
      <div className="grid gap-2 sm:grid-cols-2">
        <Field
          label="School"
          value={entry.school}
          onChange={(school) => onChange({ ...entry, school })}
        />
        <Field
          label="Degree"
          value={entry.degree ?? ''}
          onChange={(degree) => onChange({ ...entry, degree })}
        />
        <Field
          label="Field"
          value={entry.field ?? ''}
          onChange={(field) => onChange({ ...entry, field })}
        />
        <Field
          label="Years"
          value={[entry.start, entry.end].filter(Boolean).join(' – ')}
          onChange={(v) => {
            const [start, end] = v.split(/[–-]/).map((s) => s.trim());
            onChange({ ...entry, start: start ?? '', end: end ?? '' });
          }}
          placeholder="2015 – 2019"
        />
      </div>
      <div className="mt-2 flex justify-end">
        <Button type="button" size="sm" variant="ghost" onClick={onRemove}>
          Remove
        </Button>
      </div>
    </div>
  );
}

function ChipEditor({
  label,
  values,
  onChange,
  placeholder = 'Type and press Enter',
}: {
  label: string;
  values: string[];
  onChange: (v: string[]) => void;
  placeholder?: string;
}) {
  const [draft, setDraft] = useState('');
  function add() {
    const t = draft.trim();
    if (!t) return;
    if (values.some((v) => v.toLowerCase() === t.toLowerCase())) {
      setDraft('');
      return;
    }
    onChange([...values, t]);
    setDraft('');
  }
  return (
    <div>
      <p className="text-sm font-medium text-main">{label}</p>
      <div className="mt-1.5 flex flex-wrap gap-1.5">
        {values.map((v) => (
          <button
            key={v}
            type="button"
            onClick={() => onChange(values.filter((x) => x !== v))}
            className="inline-flex items-center gap-1 rounded-full bg-gray-100 px-2.5 py-0.5 text-xs text-gray-800 hover:bg-red-50 dark:bg-navy-700 dark:text-gray-200"
            title="Remove"
          >
            {v}
            <span aria-hidden>×</span>
          </button>
        ))}
      </div>
      <div className="mt-2 flex gap-2">
        <input
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault();
              add();
            }
          }}
          placeholder={placeholder}
          className="min-w-0 flex-1 rounded-md border border-muted bg-surface px-3 py-1.5 text-sm text-main"
        />
        <Button type="button" size="sm" variant="secondary" onClick={add}>
          Add
        </Button>
      </div>
    </div>
  );
}
