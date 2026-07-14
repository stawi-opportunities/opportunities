/**
 * PreferenceChat — shared Meta-style preference / intake chat.
 *
 * Embeddable on onboarding, dashboard refine, and opportunity pages.
 * All turns go through POST /me/chat (sendMeChat).
 *
 * Landing: petal mark, journey title, large card + Upload resume + chips.
 * Thread: progress header, right user pills, left assistant prose,
 *          slim sticky "Ask a question…" bar.
 */

import {
  useEffect,
  useRef,
  useState,
  type ChangeEvent,
  type FormEvent,
  type KeyboardEvent,
} from 'react';
import {
  sendMeChat,
  type OnboardingChatFieldStatus,
  type OnboardingChatFields,
  type OnboardingChatMessage,
} from '@/api/candidates';
import { uploadCV } from '@/api/profile';
import { isChatReady, missingChatFields } from '@/onboarding/chatHeuristic';
import type { PlanId } from '@/utils/plans';
import { FIELD_LABELS } from './mapFields';

export type PreferenceChatMode = 'intake' | 'refine';

export interface PreferenceChatProps {
  mode?: PreferenceChatMode;
  initialFields?: OnboardingChatFields;
  initialMessages?: OnboardingChatMessage[];
  welcome?: string;
  userName?: string;
  className?: string;
  minHeightClass?: string;
  onFieldsChange?: (
    fields: OnboardingChatFields,
    meta: {
      ready: boolean;
      missing: string[];
      messages: OnboardingChatMessage[];
      field_status?: Record<string, OnboardingChatFieldStatus>;
    }
  ) => void;
  onComplete?: (fields: OnboardingChatFields) => void | Promise<void>;
  persistDraft?: boolean;
  plan?: PlanId;
  showCompleteAction?: boolean;
  completeLabel?: string;
  compact?: boolean;
}

const REQUIRED_KEYS = [
  'target_job_title',
  'capabilities',
  'job_types',
  'salary_expectation',
  'preferred_countries',
  'experience_level',
] as const;

const STARTERS: Record<
  PreferenceChatMode,
  { label: string; text: string; icon: 'doc' | 'map' | 'brief' | 'pay' }[]
> = {
  intake: [
    {
      label: 'Describe the role I want',
      text: "I'm a senior software engineer looking for full-time remote roles. Salary USD 90k–120k. Opportunities from Kenya and Nigeria.",
      icon: 'brief',
    },
    {
      label: 'Jobs in my country',
      text: 'What full-time opportunities are available in my country? I am mid-level and prefer English posts.',
      icon: 'map',
    },
    {
      label: 'Help me find suitable roles',
      text: 'Help me find suitable roles that match my experience. I will attach my CV.',
      icon: 'doc',
    },
  ],
  refine: [
    { label: 'Change role', text: 'Update my target role to ', icon: 'brief' },
    { label: 'Salary', text: 'Update salary expectations to ', icon: 'pay' },
    { label: 'Countries', text: 'Source opportunities from these countries: ', icon: 'map' },
  ],
};

function landingTitle(name?: string, mode?: PreferenceChatMode): string {
  const n = name?.trim();
  if (mode === 'refine') {
    return n ? `Hi ${n}, what should we update?` : 'What should we update?';
  }
  return n
    ? `Hi ${n}, let's start your opportunity journey`
    : "Let's start your opportunity journey";
}

function ChipIcon({ kind }: { kind: 'doc' | 'map' | 'brief' | 'pay' }) {
  const common = {
    width: 16,
    height: 16,
    viewBox: '0 0 24 24',
    fill: 'none',
    stroke: 'currentColor',
    strokeWidth: 1.75,
    strokeLinecap: 'round' as const,
    strokeLinejoin: 'round' as const,
    'aria-hidden': true as const,
  };
  if (kind === 'doc') {
    return (
      <svg {...common}>
        <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
        <path d="M14 2v6h6M8 13h8M8 17h5" />
      </svg>
    );
  }
  if (kind === 'map') {
    return (
      <svg {...common}>
        <path d="M12 21s7-4.5 7-11a7 7 0 1 0-14 0c0 6.5 7 11 7 11z" />
        <circle cx="12" cy="10" r="2.5" />
      </svg>
    );
  }
  if (kind === 'pay') {
    return (
      <svg {...common}>
        <rect x="2" y="5" width="20" height="14" rx="2" />
        <path d="M2 10h20" />
      </svg>
    );
  }
  return (
    <svg {...common}>
      <rect x="3" y="7" width="18" height="13" rx="2" />
      <path d="M8 7V5a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
    </svg>
  );
}

function PetalMark({ size = 'lg' }: { size?: 'sm' | 'lg' }) {
  const px = size === 'sm' ? 36 : 56;
  return (
    <div
      className={`relative flex items-center justify-center ${size === 'sm' ? 'h-9 w-9' : 'mb-6 h-16 w-16'}`}
      aria-hidden
    >
      <div
        className={`absolute inset-0 rounded-full bg-gradient-to-br from-violet-300/40 via-blue-300/30 to-transparent blur-2xl ${size === 'sm' ? 'scale-125' : ''}`}
      />
      <svg width={px} height={px} viewBox="0 0 64 64" className="relative drop-shadow-sm">
        {[0, 45, 90, 135, 180, 225, 270, 315].map((deg) => (
          <ellipse
            key={deg}
            cx="32"
            cy="18"
            rx="7"
            ry="12"
            fill="url(#petalGrad)"
            transform={`rotate(${deg} 32 32)`}
            opacity="0.92"
          />
        ))}
        <defs>
          <linearGradient id="petalGrad" x1="0" y1="0" x2="1" y2="1">
            <stop offset="0%" stopColor="#a78bfa" />
            <stop offset="55%" stopColor="#6366f1" />
            <stop offset="100%" stopColor="#3b82f6" />
          </linearGradient>
        </defs>
      </svg>
    </div>
  );
}

function SendSpinner({ className = 'h-10 w-10' }: { className?: string }) {
  return (
    <svg className={`animate-spin ${className}`} viewBox="0 0 40 40" fill="none" aria-hidden>
      <circle cx="20" cy="20" r="14" stroke="#bfdbfe" strokeWidth="3" />
      <path d="M20 6a14 14 0 0 1 14 14" stroke="#ffffff" strokeWidth="3" strokeLinecap="round" />
    </svg>
  );
}

function missingPlaceholder(missing: string[], mode: PreferenceChatMode): string {
  if (mode === 'refine' && missing.length === 0) {
    return 'Ask a question…';
  }
  const first = missing[0];
  switch (first) {
    case 'target_job_title':
      return 'What role should we match you to?';
    case 'capabilities':
      return 'Upload your resume, or describe your experience…';
    case 'job_types':
      return 'Full-time, contract, or other job types?';
    case 'salary_expectation':
      return 'What are your salary expectations?';
    case 'preferred_countries':
      return 'Which countries for opportunities?';
    case 'experience_level':
      return 'Entry, junior, mid, senior, lead, or executive?';
    default:
      return 'Ask a question…';
  }
}

/** Detect CV attachment messages for compact filename chips. */
function cvFilenameFromContent(content: string): string | null {
  const attached = content.match(/^Attached CV:\s*(.+)$/i);
  if (attached?.[1]) return attached[1].trim();
  if (/\.(pdf|docx?|txt|rtf)$/i.test(content) && content.length < 160 && !content.includes('\n')) {
    return content.trim();
  }
  return null;
}

async function readTextFile(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result ?? ''));
    reader.onerror = () => reject(reader.error ?? new Error('read failed'));
    reader.readAsText(file);
  });
}

function isPlainTextCV(file: File): boolean {
  const n = file.name.toLowerCase();
  return (
    n.endsWith('.txt') ||
    n.endsWith('.md') ||
    n.endsWith('.text') ||
    file.type.startsWith('text/')
  );
}

function valueForRequired(
  key: (typeof REQUIRED_KEYS)[number],
  fields: OnboardingChatFields,
  status?: OnboardingChatFieldStatus
): string {
  if (status?.value) return status.value;
  switch (key) {
    case 'target_job_title':
      return fields.target_job_title?.trim() || '';
    case 'capabilities':
      return fields.extra_info && fields.extra_info.length >= 80 ? 'CV provided' : '';
    case 'job_types':
      return fields.job_types?.join(', ') || '';
    case 'salary_expectation': {
      if (fields.salary_min == null && fields.salary_max == null) return '';
      const cur = fields.currency || 'USD';
      const lo = fields.salary_min ?? fields.salary_max;
      const hi = fields.salary_max ?? fields.salary_min;
      return lo === hi ? `${cur} ${lo}` : `${cur} ${lo}–${hi}`;
    }
    case 'preferred_countries':
      return fields.preferred_countries?.join(', ') || fields.country || '';
    case 'experience_level':
      return fields.experience_level || '';
    default:
      return '';
  }
}

export function PreferenceChat({
  mode = 'intake',
  initialFields = {},
  initialMessages,
  welcome,
  userName,
  className = '',
  minHeightClass: _minHeightClass = 'min-h-[32rem]',
  onFieldsChange,
  onComplete,
  showCompleteAction = true,
  completeLabel,
  compact: _compact = false,
}: PreferenceChatProps) {
  void _minHeightClass;
  void _compact;
  const greeting = welcome ?? landingTitle(userName, mode);
  const [messages, setMessages] = useState<OnboardingChatMessage[]>(() =>
    initialMessages?.length ? initialMessages : []
  );
  const [fields, setFields] = useState<OnboardingChatFields>(initialFields);
  const [missing, setMissing] = useState<string[]>(() => missingChatFields(initialFields));
  const [fieldStatus, setFieldStatus] = useState<
    Record<string, OnboardingChatFieldStatus> | undefined
  >();
  const [ready, setReady] = useState(() => isChatReady(initialFields));
  const [input, setInput] = useState('');
  const [sending, setSending] = useState(false);
  const [completing, setCompleting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [touched, setTouched] = useState(false);
  const [pendingCV, setPendingCV] = useState<{ name: string; text: string } | null>(null);
  const [cvBusy, setCvBusy] = useState(false);
  const [showDetails, setShowDetails] = useState(false);
  const [waitingText, setWaitingText] = useState<string | null>(null);
  const [placementSummary, setPlacementSummary] = useState<string | null>(null);
  const bottomRef = useRef<HTMLDivElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const seededRef = useRef(false);

  // Hydrate once from parent (server draft / profile) so past turns resume.
  useEffect(() => {
    if (seededRef.current) return;
    const hasFields = Object.values(initialFields).some((v) =>
      Array.isArray(v) ? v.length > 0 : Boolean(v)
    );
    const hasMsgs = Boolean(initialMessages?.length);
    // Wait until parent finished loading when both still empty on first paint.
    if (!hasFields && !hasMsgs) return;
    seededRef.current = true;
    if (hasFields) {
      setFields(initialFields);
      setMissing(missingChatFields(initialFields));
      setReady(isChatReady(initialFields));
    }
    if (hasMsgs && initialMessages?.length) {
      setMessages(initialMessages);
    }
  }, [initialFields, initialMessages]);

  // If messages arrive after mount (async draft fetch), adopt them when we
  // still have no user turns locally — continuity without clobbering an
  // in-progress send.
  useEffect(() => {
    if (sending) return;
    if (!initialMessages?.length) return;
    setMessages((prev) => {
      const prevHasUser = prev.some((m) => m.role === 'user');
      if (prevHasUser) return prev;
      return initialMessages;
    });
  }, [initialMessages, sending]);

  useEffect(() => {
    bottomRef.current?.scrollIntoView?.({ behavior: 'smooth' });
  }, [messages, sending]);

  const hasConversation = messages.some((m) => m.role === 'user');
  const ctaLabel =
    completeLabel ?? (mode === 'intake' ? 'Continue to plans' : 'Apply updates');

  const doneCount = REQUIRED_KEYS.filter((k) => {
    if (fieldStatus?.[k]) return fieldStatus[k]!.ok;
    return !missing.includes(k) && Boolean(valueForRequired(k, fields));
  }).length;

  async function runTurn(opts: {
    message: string;
    cv_text?: string;
    cv_filename?: string;
    display?: string;
  }) {
    const message = opts.message.trim();
    const hasStructured = Boolean(opts.cv_text?.trim());
    if ((!message && !hasStructured) || sending) return;

    setSending(true);
    setError(null);
    setTouched(true);
    const history = messages.filter((m) => m.role === 'user' || m.role === 'assistant');
    const display =
      opts.display ||
      message ||
      (opts.cv_filename ? `Attached CV: ${opts.cv_filename}` : '…');
    setWaitingText(display);
    setMessages((prev) => [...prev, { role: 'user', content: display }]);
    setInput('');
    try {
      const res = await sendMeChat({
        message: message || display,
        history,
        draft: fields,
        cv_text: opts.cv_text,
        cv_filename: opts.cv_filename,
      });
      const isReady =
        typeof res.ready === 'boolean' ? res.ready : isChatReady(res.fields);
      const miss = isReady
        ? []
        : res.missing?.length
          ? res.missing
          : missingChatFields(res.fields);
      setFields(res.fields);
      setMissing(miss);
      setReady(isReady);
      setFieldStatus(res.field_status);
      if (res.placement_summary?.trim()) {
        setPlacementSummary(res.placement_summary.trim());
      }
      // Prefer full server transcript; never shrink history to a single turn
      // (local fallback used to return only the last pair).
      const appended: OnboardingChatMessage[] = [
        ...history,
        { role: 'user', content: display },
        { role: 'assistant', content: res.reply },
      ];
      const nextMsgs: OnboardingChatMessage[] =
        res.messages && res.messages.length >= appended.length ? res.messages : appended;
      setMessages(nextMsgs);
      onFieldsChange?.(res.fields, {
        ready: isReady,
        missing: miss,
        messages: nextMsgs,
        field_status: res.field_status,
      });

      if (mode === 'intake' && isReady && onComplete && !showCompleteAction) {
        await onComplete(res.fields);
      }
    } catch (e) {
      setError(e instanceof Error && e.message ? e.message : 'Something went wrong');
      setMessages((prev) => [
        ...prev,
        {
          role: 'assistant',
          content: "I couldn't process that just now. Try again in a moment.",
        },
      ]);
    } finally {
      setSending(false);
      setWaitingText(null);
      setPendingCV(null);
      textareaRef.current?.focus();
    }
  }

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    void runTurn({
      message: input,
      cv_text: pendingCV?.text,
      cv_filename: pendingCV?.name,
    });
  }

  function onKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      void runTurn({
        message: input,
        cv_text: pendingCV?.text,
        cv_filename: pendingCV?.name,
      });
    }
  }

  async function onPickCV(e: ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file) return;
    setCvBusy(true);
    setError(null);
    try {
      let text = '';
      if (isPlainTextCV(file)) {
        text = await readTextFile(file);
      } else {
        const up = await uploadCV(file);
        text = up.extracted_text ?? '';
        if (!text.trim()) {
          throw new Error(
            'Could not read text from that file. Try PDF/DOCX/TXT, or paste your CV.'
          );
        }
      }
      if (text.trim().length < 40) {
        throw new Error('That file looks empty. Paste your CV or try another file.');
      }
      setPendingCV({ name: file.name, text: text.trim() });
      await runTurn({
        message: input.trim() || `I've attached my CV (${file.name}).`,
        cv_text: text.trim(),
        cv_filename: file.name,
        display: `Attached CV: ${file.name}`,
      });
    } catch (err) {
      setError(err instanceof Error ? err.message : 'CV upload failed');
    } finally {
      setCvBusy(false);
    }
  }

  async function handleComplete() {
    if (!onComplete || completing) return;
    if (mode === 'intake' && !ready) return;
    if (mode === 'refine' && !fields.target_job_title?.trim() && !touched) return;
    setCompleting(true);
    setError(null);
    try {
      await onComplete(fields);
    } catch (e) {
      setError(e instanceof Error && e.message ? e.message : 'Could not save preferences');
    } finally {
      setCompleting(false);
    }
  }

  const canComplete =
    mode === 'intake'
      ? ready
      : Boolean(fields.target_job_title?.trim()) || (touched && Object.keys(fields).length > 0);

  const canSend = Boolean(input.trim() || pendingCV) && !sending && !completing && !cvBusy;
  const starters = STARTERS[mode];
  const isWaiting = sending || cvBusy;

  const sendControl = isWaiting ? (
    <button
      type="button"
      disabled
      className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-blue-200 shadow-sm dark:bg-blue-800/60"
      aria-label="Waiting for response"
      aria-busy="true"
    >
      <SendSpinner />
    </button>
  ) : (
    <button
      type="submit"
      disabled={!canSend}
      className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-blue-600 text-white shadow-sm transition hover:bg-blue-500 disabled:bg-stone-200 disabled:text-stone-400 dark:disabled:bg-navy-700 dark:disabled:text-navy-500"
      aria-label="Send"
    >
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" aria-hidden>
        <path
          d="M12 19V5M5 12l7-7 7 7"
          stroke="currentColor"
          strokeWidth="2.2"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </svg>
    </button>
  );

  /** Landing: Meta-style outlined card — textarea + Upload resume + send as one unit. */
  const landingComposer = (
    <form onSubmit={onSubmit} className="mx-auto w-full max-w-xl">
      {error && (
        <p className="mb-2 text-center text-xs text-red-600" role="alert">
          {error}
        </p>
      )}
      <div className="rounded-2xl border border-stone-200 bg-white px-4 pb-3 pt-3.5 shadow-[0_4px_24px_rgba(15,23,42,0.06)] outline-none dark:border-navy-600 dark:bg-navy-900">
        <textarea
          ref={textareaRef}
          value={waitingText ?? input}
          onChange={(e) => {
            if (isWaiting) return;
            setInput(e.target.value);
          }}
          onKeyDown={onKeyDown}
          rows={3}
          placeholder="Share what you are looking for…"
          disabled={isWaiting || completing}
          readOnly={isWaiting}
          className="preference-chat-input w-full resize-none border-0 bg-transparent px-1 text-[16px] leading-relaxed text-stone-900 placeholder:text-stone-400 shadow-none outline-none ring-0 focus:border-transparent focus:outline-none focus:ring-0 focus:shadow-none focus-visible:outline-none focus-visible:ring-0 dark:text-white dark:placeholder:text-stone-500"
          aria-label="Describe what you want"
          aria-busy={isWaiting}
        />
        {pendingCV && !isWaiting && (
          <div className="mb-2 flex items-center gap-2 px-1 text-xs text-blue-600">
            <span className="truncate">{pendingCV.name}</span>
            <button type="button" className="underline" onClick={() => setPendingCV(null)}>
              Remove
            </button>
          </div>
        )}
        <div className="mt-1 flex items-center justify-between gap-3 border-t border-stone-100 pt-2.5 dark:border-navy-700">
          <input
            ref={fileRef}
            type="file"
            accept=".pdf,.docx,.txt,.rtf,.md,text/plain,application/pdf"
            className="hidden"
            onChange={(e) => void onPickCV(e)}
          />
          <button
            type="button"
            onClick={() => fileRef.current?.click()}
            disabled={isWaiting}
            className="inline-flex items-center gap-1.5 rounded-full border border-blue-100 bg-blue-50 px-3.5 py-1.5 text-sm font-medium text-blue-600 transition hover:bg-blue-100 disabled:opacity-50 dark:border-blue-900/60 dark:bg-blue-950/50 dark:text-blue-300"
            aria-label="Upload resume"
          >
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" aria-hidden>
              <path
                d="M21.44 11.05l-8.49 8.49a5.25 5.25 0 01-7.42-7.42l9.19-9.19a3.5 3.5 0 014.95 4.95l-9.2 9.19a1.75 1.75 0 01-2.47-2.47l8.49-8.48"
                stroke="currentColor"
                strokeWidth="1.75"
                strokeLinecap="round"
                strokeLinejoin="round"
              />
            </svg>
            Upload resume
          </button>
          {sendControl}
        </div>
      </div>
    </form>
  );

  /** Thread: fixed to viewport bottom — paperclip + Ask a question + send. */
  const threadComposer = (
    <form onSubmit={onSubmit} className="mx-auto w-full max-w-2xl px-4 pb-[max(1.25rem,env(safe-area-inset-bottom))] pt-2">
      {error && (
        <p className="mb-2 text-center text-xs text-red-600" role="alert">
          {error}
        </p>
      )}
      <div className="flex items-center gap-2 rounded-full border border-stone-200 bg-white px-2 py-1.5 shadow-[0_4px_24px_rgba(15,23,42,0.06)] outline-none dark:border-navy-600 dark:bg-navy-900 dark:shadow-black/40">
        <input
          ref={fileRef}
          type="file"
          accept=".pdf,.docx,.txt,.rtf,.md,text/plain,application/pdf"
          className="hidden"
          onChange={(e) => void onPickCV(e)}
        />
        <button
          type="button"
          onClick={() => fileRef.current?.click()}
          disabled={isWaiting}
          className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full text-stone-400 transition hover:bg-stone-50 hover:text-blue-600 disabled:opacity-40"
          aria-label="Upload resume"
          title="Upload resume"
        >
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" aria-hidden>
            <path
              d="M21.44 11.05l-8.49 8.49a5.25 5.25 0 01-7.42-7.42l9.19-9.19a3.5 3.5 0 014.95 4.95l-9.2 9.19a1.75 1.75 0 01-2.47-2.47l8.49-8.48"
              stroke="currentColor"
              strokeWidth="1.75"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
        </button>
        <input
          type="text"
          value={waitingText ?? input}
          onChange={(e) => {
            if (isWaiting) return;
            setInput(e.target.value);
          }}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault();
              void runTurn({
                message: input,
                cv_text: pendingCV?.text,
                cv_filename: pendingCV?.name,
              });
            }
          }}
          placeholder={missingPlaceholder(missing, mode)}
          disabled={isWaiting || completing}
          readOnly={isWaiting}
          className="preference-chat-input min-w-0 flex-1 border-0 bg-transparent py-2 text-[15px] text-stone-900 placeholder:text-stone-400 shadow-none outline-none ring-0 focus:border-transparent focus:outline-none focus:ring-0 focus:shadow-none focus-visible:outline-none focus-visible:ring-0 dark:text-white dark:placeholder:text-stone-500"
          aria-label="Describe what you want"
          aria-busy={isWaiting}
        />
        {sendControl}
      </div>
      {pendingCV && !isWaiting && (
        <p className="mt-1.5 text-center text-xs text-blue-600">
          Ready: {pendingCV.name}{' '}
          <button type="button" className="underline" onClick={() => setPendingCV(null)}>
            Remove
          </button>
        </p>
      )}
      {showCompleteAction && canComplete && onComplete && (
        <div className="mt-2 flex justify-center">
          <button
            type="button"
            onClick={() => void handleComplete()}
            disabled={completing}
            className="text-xs font-semibold text-blue-600 hover:text-blue-700 disabled:opacity-50 dark:text-blue-400"
          >
            {completing ? 'Saving…' : ctaLabel}
          </button>
        </div>
      )}
    </form>
  );

  // ── Landing: centered card with clear input ───────────────────────────
  if (!hasConversation) {
    return (
      <div
        className={`relative flex h-full min-h-[min(100dvh,40rem)] flex-1 flex-col items-center justify-center overflow-y-auto bg-stone-50/80 px-4 py-8 sm:px-6 sm:py-10 dark:bg-navy-950 ${className}`}
      >
        <div
          className="pointer-events-none absolute left-1/2 top-[22%] h-72 w-72 -translate-x-1/2 rounded-full bg-gradient-to-b from-violet-200/45 via-blue-100/35 to-transparent blur-3xl dark:from-violet-900/30 dark:via-blue-900/20"
          aria-hidden
        />
        {/* Framed chat card: top padding for petal, generous bottom padding under chips */}
        <div className="relative mx-auto w-full max-w-2xl rounded-3xl border border-stone-200/90 bg-white px-5 pt-12 pb-14 shadow-[0_8px_40px_rgba(15,23,42,0.05)] sm:px-10 sm:pt-14 sm:pb-16 dark:border-navy-700 dark:bg-navy-900 dark:shadow-black/20">
          <div className="flex w-full flex-col items-center pb-2">
            <PetalMark />
            <h2 className="max-w-lg text-center text-[1.65rem] font-semibold tracking-tight text-stone-900 dark:text-white sm:text-[1.85rem]">
              {greeting}
            </h2>
            <p className="mt-2 max-w-md text-center text-sm leading-relaxed text-stone-500 dark:text-stone-400">
              {mode === 'refine'
                ? 'Update role, CV, salary, or countries — then apply.'
                : 'Share what you are looking for and be exact enough to avoid missing great opportunities out there.'}
            </p>

            <div className="mt-8 w-full">{landingComposer}</div>

            <div className="mt-5 mb-2 grid w-full grid-cols-1 gap-2.5 sm:grid-cols-3">
              {starters.map((s) => (
                <button
                  key={s.label}
                  type="button"
                  onClick={() => {
                    setInput(s.text);
                    textareaRef.current?.focus();
                  }}
                  className="flex items-start gap-2.5 rounded-2xl border border-stone-100 bg-stone-50/80 px-3.5 py-3 text-left text-sm text-stone-600 transition hover:border-stone-200 hover:bg-white hover:shadow-sm dark:border-navy-700 dark:bg-navy-950/50 dark:text-stone-300 dark:hover:bg-navy-800"
                >
                  <span className="mt-0.5 shrink-0 text-stone-400">
                    <ChipIcon kind={s.icon} />
                  </span>
                  <span className="leading-snug">{s.label}</span>
                </button>
              ))}
            </div>
          </div>
        </div>
      </div>
    );
  }

  // ── Active conversation: scrollable thread + composer (page footer remains below)
  return (
    <div
      className={`relative flex min-h-[32rem] flex-1 flex-col bg-white outline-none dark:bg-navy-950 ${className}`}
    >
      {/* Minimal header — no box outline */}
      <div className="mx-auto flex w-full max-w-2xl shrink-0 items-center justify-between gap-3 px-4 pb-2 pt-5">
        <div className="flex min-w-0 items-center gap-2">
          <PetalMark size="sm" />
          <button
            type="button"
            onClick={() => setShowDetails((v) => !v)}
            className="truncate text-left text-xs text-stone-500 hover:text-stone-700 dark:text-stone-400"
            aria-expanded={showDetails}
          >
            Matching profile {doneCount}/{REQUIRED_KEYS.length}
            {!ready && missing[0]
              ? ` · need ${FIELD_LABELS[missing[0]] ?? missing[0]}`
              : ready
                ? ' · ready'
                : ''}
            {showDetails ? ' · hide' : ''}
          </button>
        </div>
        {showCompleteAction && canComplete && onComplete && (
          <button
            type="button"
            onClick={() => void handleComplete()}
            disabled={completing}
            className="shrink-0 text-xs font-semibold text-blue-600 hover:text-blue-700 disabled:opacity-50"
          >
            {completing ? 'Saving…' : ctaLabel}
          </button>
        )}
      </div>
      {showDetails && (
        <div className="mx-auto mb-2 w-full max-w-2xl space-y-2 px-4 text-xs">
          <ul className="space-y-1">
            {REQUIRED_KEYS.map((key) => {
              const st = fieldStatus?.[key];
              const ok = st
                ? st.ok
                : !missing.includes(key) && Boolean(valueForRequired(key, fields));
              const value = valueForRequired(key, fields, st);
              return (
                <li key={key} className="flex items-baseline gap-2">
                  <span className={ok ? 'text-emerald-600' : 'text-amber-600'}>
                    {ok ? '✓' : '○'}
                  </span>
                  <span className="text-stone-500">{FIELD_LABELS[key] ?? key}</span>
                  {ok && value ? (
                    <span className="truncate text-stone-800 dark:text-stone-200">{value}</span>
                  ) : (
                    <span className="text-stone-400">{st?.reason || 'needed'}</span>
                  )}
                </li>
              );
            })}
          </ul>
          {placementSummary && (
            <div className="rounded-xl bg-[#f0f2f5] px-3 py-2 text-[11px] leading-relaxed text-stone-600 dark:bg-navy-800 dark:text-stone-300">
              <p className="mb-1 font-semibold uppercase tracking-wide text-stone-500">
                Placement summary
              </p>
              <p className="line-clamp-6 whitespace-pre-wrap">{placementSummary}</p>
            </div>
          )}
        </div>
      )}

      <div
        className="mx-auto flex w-full max-w-2xl min-h-0 flex-1 flex-col overflow-y-auto bg-white px-4 pb-4 pt-4 dark:bg-navy-950"
        role="log"
        aria-live="polite"
        aria-label="Preference conversation"
      >
        <div className="flex flex-col gap-5">
          {messages.map((m, i) => {
            if (m.role === 'user') {
              const cvName = cvFilenameFromContent(m.content);
              return (
                <div key={`u-${i}`} className="flex justify-end">
                  {/* Meta-style: soft gray pill on the right */}
                  <div
                    className={`max-w-[min(85%,28rem)] rounded-full px-4 py-2.5 text-[15px] leading-relaxed text-stone-900 dark:text-stone-100 ${
                      cvName
                        ? 'rounded-2xl bg-[#f0f2f5] font-medium dark:bg-navy-800'
                        : 'bg-[#f0f2f5] dark:bg-navy-800'
                    }`}
                  >
                    {cvName ? (
                      <span className="inline-flex items-center gap-2">
                        <span className="flex h-6 w-6 items-center justify-center rounded-full bg-white text-stone-500 dark:bg-navy-700">
                          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" aria-hidden>
                            <path
                              d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"
                              stroke="currentColor"
                              strokeWidth="1.75"
                            />
                            <path d="M14 2v6h6" stroke="currentColor" strokeWidth="1.75" />
                          </svg>
                        </span>
                        {cvName}
                      </span>
                    ) : (
                      m.content
                    )}
                  </div>
                </div>
              );
            }
            return (
              <div key={`a-${i}`} className="flex justify-start">
                {/* Meta-style: plain dark prose, no bubble */}
                <div className="max-w-[min(100%,36rem)] whitespace-pre-wrap text-[15px] leading-[1.65] text-stone-900 dark:text-stone-100">
                  {m.content}
                </div>
              </div>
            );
          })}
          {sending && (
            <div className="flex items-center gap-2 text-sm text-stone-400" aria-live="polite">
              <SendSpinner className="h-5 w-5" />
              <span>Matching…</span>
            </div>
          )}
          <div ref={bottomRef} />
        </div>
      </div>

      {/* Pinned to bottom of the chat shell (not a framed mid-page box) */}
      <div className="shrink-0 bg-white/95 px-0 pb-4 pt-2 backdrop-blur-sm dark:bg-navy-950/95">
        {threadComposer}
      </div>
    </div>
  );
}
