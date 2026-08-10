/**
 * Meta-style sticky side chat for opportunity detail pages.
 * Visible as a right rail on xl+; on smaller screens a FAB opens a slide-over.
 *
 * One shared conversation across all job listings (not per-job sessions).
 * The current page listing is always sent as structured context for the LLM,
 * and a reusable job card widget is shown when referencing a post.
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
  chatErrorMessage,
  fetchOnboardingDraft,
  sendMeChat,
  type OnboardingChatFields,
} from '@/api/candidates';
import { uploadCV } from '@/api/profile';
import type { OpportunitySnapshot } from '@/types/snapshot';
import { profileToChatFields } from '@/components/preference-chat/mapFields';
import {
  OpportunityChatCard,
  opportunityCardFromSnap,
  type OpportunityChatCardData,
} from '@/components/chat/OpportunityChatCard';
import {
  loadOpportunityChat,
  saveOpportunityChat,
  type OpportunityChatMessage,
} from '@/components/chat/opportunityChatStorage';
import { useCandidateProfile } from '@/hooks/useCandidateProfile';
import { useAuth } from '@/providers/AuthProvider';
import { displayUserContent } from '@/utils/chatDisplay';

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
    n.endsWith('.txt') || n.endsWith('.md') || n.endsWith('.text') || file.type.startsWith('text/')
  );
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

function lastCardSlug(messages: OpportunityChatMessage[]): string | undefined {
  for (let i = messages.length - 1; i >= 0; i--) {
    const s = messages[i]?.card?.slug;
    if (s) return s;
  }
  return undefined;
}

export function OpportunitySideChat({ snap }: { snap: OpportunitySnapshot }) {
  const { state, hasSession, login } = useAuth();
  const profileQ = useCandidateProfile();
  const [openMobile, setOpenMobile] = useState(false);
  const [messages, setMessages] = useState<OpportunityChatMessage[]>([]);
  const [fields, setFields] = useState<OnboardingChatFields>({});
  const [input, setInput] = useState('');
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [hydrated, setHydrated] = useState(false);
  const bottomRef = useRef<HTMLDivElement>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const lastSnapSlug = useRef<string | null>(null);

  const card: OpportunityChatCardData = opportunityCardFromSnap(snap);

  // Shared multi-job session: restore transcript once; when the listing changes,
  // append a "you're viewing …" turn with the job card — do not wipe history.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      const draft = await fetchOnboardingDraft();
      if (cancelled) return;
      const seeded = profileToChatFields(profileQ.data, draft.fields);
      const stored = loadOpportunityChat();
      const baseFields = { ...(stored?.fields ?? {}), ...seeded };

      setFields(baseFields);

      if (!stored?.messages?.length) {
        const welcome: OpportunityChatMessage = {
          role: 'assistant',
          content: buildWelcome(snap),
          card,
        };
        setMessages([welcome]);
        saveOpportunityChat({ messages: [welcome], fields: baseFields, updated_at: '' });
        lastSnapSlug.current = snap.slug;
        setHydrated(true);
        return;
      }

      let nextMsgs = stored.messages;
      // New listing in the shared thread → introduce it with the reusable card.
      if (lastCardSlug(nextMsgs) !== snap.slug && lastSnapSlug.current !== snap.slug) {
        nextMsgs = [
          ...nextMsgs,
          {
            role: 'assistant',
            content: buildWelcome(snap),
            card,
          },
        ];
        saveOpportunityChat({ messages: nextMsgs, fields: baseFields, updated_at: '' });
      }
      setMessages(nextMsgs);
      lastSnapSlug.current = snap.slug;
      setHydrated(true);
    })();
    return () => {
      cancelled = true;
    };
    // Depend on snap identity + profile seed, not every field.
  }, [snap.id, snap.slug, profileQ.data]);

  useEffect(() => {
    bottomRef.current?.scrollIntoView?.({ behavior: 'smooth' });
  }, [messages, sending]);

  const runTurn = useCallback(
    async (opts: { message: string; cv_text?: string; cv_filename?: string; display?: string }) => {
      const raw = opts.message.trim();
      const hasCv = Boolean(opts.cv_text?.trim());
      if ((!raw && !hasCv) || sending) return;

      const display = displayUserContent(
        opts.display || raw || (opts.cv_filename ? `Attached CV: ${opts.cv_filename}` : '…')
      );
      const apiMessage = hasCv ? raw || `I've attached my CV (${opts.cv_filename}).` : raw;

      setSending(true);
      setError(null);
      const history = messages
        .filter((m) => m.role === 'user' || m.role === 'assistant')
        .map((m) =>
          m.role === 'user'
            ? { role: m.role, content: displayUserContent(m.content) }
            : { role: m.role, content: m.content }
        );
      setMessages((prev) => [...prev, { role: 'user', content: display }]);
      setInput('');

      try {
        const res = await sendMeChat({
          message: apiMessage,
          history,
          draft: fields,
          cv_text: opts.cv_text,
          cv_filename: opts.cv_filename,
          context: 'opportunity',
          opportunity: {
            id: snap.id,
            slug: snap.slug,
            title: snap.title,
            issuing_entity: snap.issuing_entity,
            location: locationLine(snap),
            description:
              typeof snap.description === 'string' ? snap.description.slice(0, 4000) : '',
            kind: snap.kind,
            apply_url: snap.apply_url ?? undefined,
          },
        });
        setFields(res.fields);
        const replyCard: OpportunityChatCardData | undefined = res.card
          ? {
              title: res.card.title,
              subtitle: res.card.subtitle,
              href: res.card.href || card.href,
              apply_url: res.card.apply_url,
              opportunity_id: res.card.opportunity_id,
              slug: res.card.slug || snap.slug,
            }
          : card;

        const appended: OpportunityChatMessage[] = [
          ...messages.filter((m) => m.role === 'user' || m.role === 'assistant'),
          { role: 'user', content: display },
          { role: 'assistant', content: res.reply, card: replyCard },
        ];

        // Prefer server transcript for text, then re-attach cards for job widgets.
        let next: OpportunityChatMessage[];
        if (res.messages && res.messages.length >= appended.length) {
          next = res.messages.map((m, i) => {
            const base: OpportunityChatMessage = {
              role: m.role,
              content: m.role === 'user' ? displayUserContent(m.content) : m.content,
              card: m.card
                ? {
                    title: m.card.title,
                    subtitle: m.card.subtitle,
                    href: m.card.href,
                    apply_url: m.card.apply_url,
                    opportunity_id: m.card.opportunity_id,
                    slug: m.card.slug,
                  }
                : undefined,
            };
            // Last assistant turn always shows the current listing card.
            if (i === res.messages!.length - 1 && m.role === 'assistant' && !base.card) {
              base.card = replyCard;
            }
            return base;
          });
        } else {
          next = appended;
        }

        setMessages(next);
        saveOpportunityChat({ messages: next, fields: res.fields, updated_at: '' });
      } catch (e) {
        const honest = chatErrorMessage(e);
        setError(honest);
        setMessages((prev) => {
          const next = [...prev, { role: 'assistant' as const, content: honest }];
          saveOpportunityChat({ messages: next, fields, updated_at: '' });
          return next;
        });
      } finally {
        setSending(false);
        inputRef.current?.focus();
      }
    },
    [fields, messages, sending, snap, card]
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
        if (up.placement_ready) {
          setMessages((prev) => {
            const next: OpportunityChatMessage[] = [
              ...prev,
              { role: 'user', content: `Attached CV: ${file.name}` },
              {
                role: 'assistant',
                content:
                  'Your CV is processed and your profile is ready for matching. What would you like to know about this role?',
                card,
              },
            ];
            saveOpportunityChat({ messages: next, fields, updated_at: '' });
            return next;
          });
          setSending(false);
          return;
        }
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

  const needsAuth = !hasSession && state === 'unauthenticated';

  const panel = (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex shrink-0 flex-col items-center gap-2 px-5 pb-3 pt-5">
        <PetalMark />
        <p className="max-w-[17.5rem] text-center text-[11px] leading-relaxed text-stone-400">
          AI guidance is optional. Your conversation helps personalize matching and does not replace
          applying on the listing.
        </p>
      </div>

      <div
        className="min-h-0 flex-1 space-y-4 overflow-y-auto bg-white px-5 py-4 dark:bg-navy-900"
        role="log"
        aria-live="polite"
        aria-label="Opportunity assistant"
      >
        {!hydrated && <p className="text-center text-sm text-stone-400">Loading conversation…</p>}
        {messages.map((m, i) => {
          if (m.role === 'user') {
            const text = displayUserContent(m.content);
            const cvName = cvFilenameFromContent(text);
            return (
              <div key={`u-${i}`} className="flex justify-end">
                <div
                  className={`max-w-[90%] rounded-xl bg-[#f0f2f5] px-3.5 py-2 text-[13.5px] leading-relaxed text-stone-900 dark:bg-navy-800 dark:text-stone-100 ${
                    text.includes('\n') ? 'whitespace-pre-wrap' : ''
                  } ${cvName ? 'font-medium' : ''}`}
                >
                  {cvName ?? text}
                </div>
              </div>
            );
          }
          return (
            <div key={`a-${i}`} className="space-y-3">
              <div className="whitespace-pre-wrap text-[13.5px] leading-[1.65] text-stone-900 dark:text-stone-100">
                {m.content}
              </div>
              {m.card ? <OpportunityChatCard card={m.card} /> : null}
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

      <div className="shrink-0 border-t border-stone-100 bg-stone-50/50 px-4 pb-4 pt-3 dark:border-navy-800 dark:bg-navy-900/40">
        {error && (
          <p className="mb-2 text-center text-xs text-red-600" role="alert">
            {error}
          </p>
        )}
        {needsAuth ? (
          <button
            type="button"
            onClick={() => {
              void login();
            }}
            className="flex w-full items-center justify-center rounded-full border border-navy-800 bg-navy-900 px-4 py-2.5 text-sm font-medium text-white"
          >
            Chat
          </button>
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
                    strokeWidth="2"
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
      {/* Desktop rail */}
      <aside className="hidden xl:flex xl:h-[min(100vh-6rem,720px)] xl:w-[22rem] xl:shrink-0 xl:flex-col xl:overflow-hidden xl:rounded-2xl xl:border xl:border-stone-200 xl:bg-white xl:shadow-sm dark:xl:border-navy-700 dark:xl:bg-navy-900">
        {panel}
      </aside>

      {/* Mobile FAB + drawer */}
      <div className="xl:hidden">
        <button
          type="button"
          onClick={() => setOpenMobile(true)}
          className="fixed bottom-5 right-5 z-40 flex h-14 w-14 items-center justify-center rounded-full bg-blue-600 text-white shadow-lg"
          aria-label="Open job assistant"
        >
          <PetalMark />
        </button>
        {openMobile && (
          <div className="fixed inset-0 z-50 flex flex-col bg-black/40" role="dialog" aria-modal>
            <button
              type="button"
              className="min-h-[20%] flex-1"
              aria-label="Close"
              onClick={() => setOpenMobile(false)}
            />
            <div className="flex h-[80vh] flex-col overflow-hidden rounded-t-2xl bg-white shadow-xl dark:bg-navy-900">
              <div className="flex justify-end px-3 pt-2">
                <button
                  type="button"
                  className="rounded-full px-3 py-1 text-sm text-stone-500"
                  onClick={() => setOpenMobile(false)}
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
