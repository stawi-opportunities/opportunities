/**
 * Meta-style sticky side chat for opportunity detail pages.
 * Visible as a right rail on xl+; on smaller screens a FAB opens a slide-over.
 * Reuses POST /me/chat so resume + preference context continues across pages.
 */

import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ChangeEvent,
  type FormEvent,
  type KeyboardEvent,
} from 'react';
import {
  fetchOnboardingDraft,
  sendMeChat,
  type OnboardingChatFields,
  type OnboardingChatMessage,
} from '@/api/candidates';
import { uploadCV } from '@/api/profile';
import type { OpportunitySnapshot } from '@/types/snapshot';
import { profileToChatFields } from '@/components/preference-chat/mapFields';
import { useCandidateProfile } from '@/hooks/useCandidateProfile';
import { useAuth } from '@/providers/AuthProvider';

function locationLine(snap: OpportunitySnapshot): string {
  const parts = [
    snap.anchor_location?.city,
    snap.anchor_location?.region,
    snap.anchor_location?.country,
  ].filter(Boolean);
  if (parts.length) return parts.join(', ');
  if (snap.remote) return 'Remote';
  return '';
}

function buildWelcome(snap: OpportunitySnapshot): string {
  const where = locationLine(snap);
  return (
    `You're viewing **${snap.title}** at ${snap.issuing_entity}` +
    (where ? ` (${where})` : '') +
    `. Ask anything about this opportunity, your fit, or how to tailor your search. ` +
    `Upload a resume for more personalized guidance.`
  ).replace(/\*\*/g, '');
}

function opportunityCardFromSnap(snap: OpportunitySnapshot) {
  return {
    title: snap.title,
    subtitle: [snap.issuing_entity, locationLine(snap)].filter(Boolean).join(' · '),
    href: typeof window !== 'undefined' ? window.location.pathname : `/${snap.slug}/`,
    apply_url: snap.apply_url,
  };
}

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
  return n.endsWith('.txt') || n.endsWith('.md') || n.endsWith('.text') || file.type.startsWith('text/');
}

function PetalMark() {
  return (
    <div className="relative flex h-8 w-8 items-center justify-center" aria-hidden>
      <svg width="28" height="28" viewBox="0 0 64 64" className="drop-shadow-sm">
        {[0, 45, 90, 135, 180, 225, 270, 315].map((deg) => (
          <ellipse
            key={deg}
            cx="32"
            cy="18"
            rx="7"
            ry="12"
            fill="url(#sidePetal)"
            transform={`rotate(${deg} 32 32)`}
            opacity="0.92"
          />
        ))}
        <defs>
          <linearGradient id="sidePetal" x1="0" y1="0" x2="1" y2="1">
            <stop offset="0%" stopColor="#a78bfa" />
            <stop offset="55%" stopColor="#6366f1" />
            <stop offset="100%" stopColor="#3b82f6" />
          </linearGradient>
        </defs>
      </svg>
    </div>
  );
}

function SendSpinner() {
  return (
    <svg className="h-9 w-9 animate-spin" viewBox="0 0 40 40" fill="none" aria-hidden>
      <circle cx="20" cy="20" r="14" stroke="#bfdbfe" strokeWidth="3" />
      <path d="M20 6a14 14 0 0 1 14 14" stroke="#fff" strokeWidth="3" strokeLinecap="round" />
    </svg>
  );
}

export function OpportunitySideChat({ snap }: { snap: OpportunitySnapshot }) {
  const { state } = useAuth();
  const profileQ = useCandidateProfile();
  const [openMobile, setOpenMobile] = useState(false);
  const [messages, setMessages] = useState<OnboardingChatMessage[]>([]);
  const [fields, setFields] = useState<OnboardingChatFields>({});
  const [input, setInput] = useState('');
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [hydrated, setHydrated] = useState(false);
  const [showCurrentCard, setShowCurrentCard] = useState(true);
  const bottomRef = useRef<HTMLDivElement>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  const card = opportunityCardFromSnap(snap);

  // Load prior onboarding conversation for continuity.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      const draft = await fetchOnboardingDraft();
      if (cancelled) return;
      const seeded = profileToChatFields(profileQ.data, draft.fields);
      setFields(seeded);
      const prior = draft.messages ?? [];
      if (prior.length > 0) {
        setMessages(prior);
      } else {
        setMessages([{ role: 'assistant', content: buildWelcome(snap) }]);
      }
      setHydrated(true);
    })();
    return () => {
      cancelled = true;
    };
  }, [snap.id, snap.slug, snap.title, snap.issuing_entity, profileQ.data]);

  useEffect(() => {
    bottomRef.current?.scrollIntoView?.({ behavior: 'smooth' });
  }, [messages, sending]);

  const runTurn = useCallback(
    async (opts: { message: string; cv_text?: string; cv_filename?: string; display?: string }) => {
      const raw = opts.message.trim();
      const hasCv = Boolean(opts.cv_text?.trim());
      if ((!raw && !hasCv) || sending) return;

      const display =
        opts.display ||
        raw ||
        (opts.cv_filename ? `Attached CV: ${opts.cv_filename}` : '…');

      // Include opportunity context for the model without cluttering the UI bubble.
      const contextPrefix = `[Viewing opportunity: "${snap.title}" at ${snap.issuing_entity}${
        locationLine(snap) ? `, ${locationLine(snap)}` : ''
      }. slug=${snap.slug}]\n\n`;
      const apiMessage = hasCv
        ? `${contextPrefix}${raw || `I've attached my CV (${opts.cv_filename}).`}`
        : `${contextPrefix}${raw}`;

      setSending(true);
      setError(null);
      const history = messages.filter((m) => m.role === 'user' || m.role === 'assistant');
      setMessages((prev) => [...prev, { role: 'user', content: display }]);
      setInput('');

      try {
        const res = await sendMeChat({
          message: apiMessage,
          history,
          draft: fields,
          cv_text: opts.cv_text,
          cv_filename: opts.cv_filename,
        });
        setFields(res.fields);
        const appended: OnboardingChatMessage[] = [
          ...history,
          { role: 'user', content: display },
          { role: 'assistant', content: res.reply },
        ];
        const next =
          res.messages && res.messages.length >= appended.length ? res.messages : appended;
        setMessages(next);
        setShowCurrentCard(true);
      } catch (e) {
        setError(e instanceof Error ? e.message : 'Something went wrong');
        setMessages((prev) => [
          ...prev,
          {
            role: 'assistant',
            content: "I couldn't process that just now. Try again in a moment.",
          },
        ]);
      } finally {
        setSending(false);
        inputRef.current?.focus();
      }
    },
    [fields, messages, sending, snap]
  );

  async function onPickCV(e: ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file) return;
    setSending(true);
    setError(null);
    try {
      let text = '';
      if (isPlainTextCV(file)) {
        text = await readTextFile(file);
      } else {
        const up = await uploadCV(file);
        text = up.extracted_text ?? '';
        if (!text.trim()) throw new Error('Could not read that file. Try PDF, DOCX, or TXT.');
      }
      if (text.trim().length < 40) throw new Error('That file looks empty.');
      await runTurn({
        message: `I've attached my CV (${file.name}) while reviewing ${snap.title}.`,
        cv_text: text.trim(),
        cv_filename: file.name,
        display: `Attached CV: ${file.name}`,
      });
    } catch (err) {
      setError(err instanceof Error ? err.message : 'CV upload failed');
      setSending(false);
    }
  }

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    void runTurn({ message: input });
  }

  function onKeyDown(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'Enter') {
      e.preventDefault();
      void runTurn({ message: input });
    }
  }

  const needsAuth = state === 'unauthenticated';

  /**
   * Panel chrome: intentional outer outline + inner padding so the chat reads
   * as a distinct card (Meta-style rail), separate from the listing.
   */
  const panel = (
    <div className="flex h-full min-h-0 flex-col">
      {/* Header */}
      <div className="flex shrink-0 flex-col items-center gap-2 px-5 pb-3 pt-5">
        <PetalMark />
        <p className="max-w-[17.5rem] text-center text-[11px] leading-relaxed text-stone-400">
          AI guidance is optional. Your conversation helps personalize matching and does not replace
          applying on the listing.
        </p>
      </div>

      {/* Transcript — Meta Careers: gray user pill, plain assistant text, white canvas */}
      <div
        className="min-h-0 flex-1 space-y-4 overflow-y-auto bg-white px-5 py-4 dark:bg-navy-900"
        role="log"
        aria-live="polite"
        aria-label="Opportunity assistant"
      >
        {!hydrated && (
          <p className="text-center text-sm text-stone-400">Loading conversation…</p>
        )}
        {messages.map((m, i) => {
          if (m.role === 'user') {
            const cvName = cvFilenameFromContent(m.content);
            return (
              <div key={`u-${i}`} className="flex justify-end">
                <div
                  className={`max-w-[90%] rounded-full bg-[#f0f2f5] px-3.5 py-2 text-[13.5px] leading-relaxed text-stone-900 dark:bg-navy-800 dark:text-stone-100 ${
                    cvName ? 'rounded-2xl font-medium' : ''
                  }`}
                >
                  {cvName ?? m.content}
                </div>
              </div>
            );
          }
          return (
            <div key={`a-${i}`} className="space-y-3">
              <div className="whitespace-pre-wrap text-[13.5px] leading-[1.65] text-stone-900 dark:text-stone-100">
                {m.content}
              </div>
              {showCurrentCard && i === messages.length - 1 && (
                <a
                  href={card.apply_url || card.href}
                  target={card.apply_url ? '_blank' : undefined}
                  rel={card.apply_url ? 'noopener noreferrer' : undefined}
                  className="block rounded-xl border border-stone-200 bg-white px-3.5 py-3 shadow-sm transition hover:border-blue-300 hover:shadow"
                >
                  <p className="text-sm font-semibold text-stone-900">{card.title}</p>
                  <p className="mt-0.5 text-xs text-stone-500">{card.subtitle}</p>
                  {card.apply_url && (
                    <p className="mt-1.5 text-xs font-medium text-blue-600">Apply →</p>
                  )}
                </a>
              )}
            </div>
          );
        })}
        {sending && (
          <div className="flex items-center gap-2 text-sm text-stone-400">
            <SendSpinner />
            <span className="sr-only">Thinking</span>
          </div>
        )}
        <div ref={bottomRef} />
      </div>

      {/* Composer — clear outline + margin so the input is an obvious affordance */}
      <div className="shrink-0 border-t border-stone-100 bg-stone-50/50 px-4 pb-4 pt-3 dark:border-navy-800 dark:bg-navy-900/40">
        {error && (
          <p className="mb-2 text-center text-xs text-red-600" role="alert">
            {error}
          </p>
        )}
        {needsAuth ? (
          <a
            href="/auth/login/"
            className="flex items-center justify-center rounded-full border border-navy-800 bg-navy-900 px-4 py-2.5 text-sm font-medium text-white"
          >
            Sign in to chat
          </a>
        ) : (
          <form
            onSubmit={onSubmit}
            className="flex items-center gap-1.5 rounded-full border border-stone-300 bg-white px-2 py-1.5 shadow-sm outline-none dark:border-navy-600 dark:bg-navy-950"
          >
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
              disabled={sending}
              className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full text-stone-400 hover:bg-stone-50 hover:text-blue-600 disabled:opacity-40"
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
              ref={inputRef}
              type="text"
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={onKeyDown}
              placeholder="Ask a question…"
              disabled={sending}
              className="preference-chat-input min-w-0 flex-1 border-0 bg-transparent py-2 text-sm text-stone-900 placeholder:text-stone-400 shadow-none outline-none ring-0 focus:border-transparent focus:outline-none focus:ring-0 focus:shadow-none focus-visible:outline-none focus-visible:ring-0"
              aria-label="Ask a question about this opportunity"
            />
            {sending ? (
              <span className="flex h-9 w-9 items-center justify-center rounded-full bg-blue-200">
                <SendSpinner />
              </span>
            ) : (
              <button
                type="submit"
                disabled={!input.trim()}
                className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-blue-600 text-white disabled:bg-stone-200 disabled:text-stone-400"
                aria-label="Send"
              >
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden>
                  <path
                    d="M12 19V5M5 12l7-7 7 7"
                    stroke="currentColor"
                    strokeWidth="2.2"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  />
                </svg>
              </button>
            )}
          </form>
        )}
      </div>
    </div>
  );

  return (
    <>
      {/* Desktop: sticky side rail — outer outline defines the chat card */}
      <aside
        className="hidden xl:flex xl:w-[min(100%,24rem)] xl:shrink-0 xl:flex-col xl:pl-2"
        aria-label="Opportunity assistant"
      >
        <div className="sticky top-20 flex h-[calc(100dvh-6.5rem)] flex-col overflow-hidden rounded-2xl border border-stone-200 bg-white shadow-[0_8px_30px_rgba(15,23,42,0.06)] dark:border-navy-600 dark:bg-navy-900 dark:shadow-black/20">
          {panel}
        </div>
      </aside>

      {/* Mobile / tablet: FAB + slide-over with same outlined shell */}
      <div className="xl:hidden">
        <button
          type="button"
          onClick={() => setOpenMobile(true)}
          className="fixed bottom-5 right-5 z-40 flex items-center gap-2 rounded-full border border-blue-700 bg-blue-600 px-4 py-3 text-sm font-semibold text-white shadow-lg shadow-blue-600/25"
        >
          <PetalMark />
          Ask AI
        </button>
        {openMobile && (
          <div className="fixed inset-0 z-50 flex flex-col bg-black/30" role="dialog" aria-modal>
            <button
              type="button"
              className="flex-1"
              aria-label="Close assistant"
              onClick={() => setOpenMobile(false)}
            />
            <div className="mx-3 mb-3 flex h-[min(88dvh,40rem)] flex-col overflow-hidden rounded-2xl border border-stone-200 bg-white shadow-2xl dark:border-navy-600 dark:bg-navy-900">
              <div className="flex items-center justify-between border-b border-stone-100 px-4 py-2.5 dark:border-navy-700">
                <span className="text-sm font-medium text-stone-700 dark:text-stone-200">
                  Assistant
                </span>
                <button
                  type="button"
                  onClick={() => setOpenMobile(false)}
                  className="rounded-full px-3 py-1 text-sm text-stone-500 hover:bg-stone-50 dark:hover:bg-navy-800"
                >
                  Close
                </button>
              </div>
              <div className="min-h-0 flex-1">{panel}</div>
            </div>
          </div>
        )}
      </div>
    </>
  );
}
