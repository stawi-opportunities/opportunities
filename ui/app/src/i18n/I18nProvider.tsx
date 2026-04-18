import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import {
  CATALOG,
  LANG_LABEL,
  RTL_LANGS,
  SUPPORTED_LANGS,
  type LangCode,
  type StringKey,
} from "./strings";

const STORAGE_KEY = "stawi.lang";
const EVENT = "stawi:lang-change";

interface I18nContextValue {
  lang: LangCode;
  setLang: (code: LangCode) => void;
  t: (key: StringKey, fallback?: string) => string;
  dir: "ltr" | "rtl";
  labelFor: (code: LangCode) => string;
  languages: readonly LangCode[];
}

const I18nContext = createContext<I18nContextValue | null>(null);

// resolveInitialLang picks the starting language with this priority:
// 1. explicit localStorage entry (user's previous choice)
// 2. navigator.language prefix if it maps to a supported code
// 3. "en"
//
// Any unknown or unsupported code falls through to English — we never
// render an empty UI on a code we don't have a catalog for.
function resolveInitialLang(): LangCode {
  if (typeof window === "undefined") return "en";
  const stored = window.localStorage.getItem(STORAGE_KEY);
  if (stored && isSupported(stored)) return stored;
  const nav = window.navigator?.language?.split("-")[0]?.toLowerCase() ?? "";
  if (isSupported(nav)) return nav;
  return "en";
}

function isSupported(code: string): code is LangCode {
  return (SUPPORTED_LANGS as readonly string[]).includes(code);
}

// Apply html[lang] and html[dir] on every lang change so Tailwind's
// rtl:/ltr: variants and screen readers both see the right attribute.
function applyHtmlAttrs(lang: LangCode) {
  if (typeof document === "undefined") return;
  document.documentElement.lang = lang;
  document.documentElement.dir = RTL_LANGS.has(lang) ? "rtl" : "ltr";
}

export function I18nProvider({ children }: { children: ReactNode }) {
  const [lang, setLangState] = useState<LangCode>(() => resolveInitialLang());

  // Apply attributes on mount and every subsequent change.
  useEffect(() => {
    applyHtmlAttrs(lang);
  }, [lang]);

  // Cross-island sync. Multiple React roots are mounted on the same page
  // (nav, job detail, search). A language switch in the nav root needs to
  // flip the job detail root immediately — window events give us that
  // without a shared store.
  useEffect(() => {
    if (typeof window === "undefined") return;
    const onChange = (e: Event) => {
      const next = (e as CustomEvent<LangCode>).detail;
      if (next && isSupported(next) && next !== lang) setLangState(next);
    };
    window.addEventListener(EVENT, onChange);
    return () => window.removeEventListener(EVENT, onChange);
  }, [lang]);

  const setLang = useCallback((next: LangCode) => {
    if (!isSupported(next)) return;
    if (typeof window !== "undefined") {
      window.localStorage.setItem(STORAGE_KEY, next);
      window.dispatchEvent(new CustomEvent(EVENT, { detail: next }));
    }
    setLangState(next);
  }, []);

  const t = useCallback(
    (key: StringKey, fallback?: string) => {
      const catalog = CATALOG[lang] ?? CATALOG.en;
      return catalog[key] ?? CATALOG.en[key] ?? fallback ?? String(key);
    },
    [lang],
  );

  const value = useMemo<I18nContextValue>(
    () => ({
      lang,
      setLang,
      t,
      dir: RTL_LANGS.has(lang) ? "rtl" : "ltr",
      labelFor: (code: LangCode) => LANG_LABEL[code],
      languages: SUPPORTED_LANGS,
    }),
    [lang, setLang, t],
  );

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n(): I18nContextValue {
  const ctx = useContext(I18nContext);
  if (!ctx) {
    // Sensible fallback so any component rendered outside the provider
    // still works (e.g. tests, stories). Not recommended in prod.
    return {
      lang: "en",
      setLang: () => {},
      t: (_k, fallback) => fallback ?? String(_k),
      dir: "ltr",
      labelFor: (code) => LANG_LABEL[code],
      languages: SUPPORTED_LANGS,
    };
  }
  return ctx;
}
