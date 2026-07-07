import { createContext, useContext, type ReactNode } from 'react';
import { CATALOG, type LangCode, type StringKey } from './strings';

interface I18nContextValue {
  lang: LangCode;
  setLang: (code: LangCode) => void;
  t: (key: StringKey, fallback?: string) => string;
  dir: 'ltr';
  labelFor: (code: LangCode) => string;
  languages: readonly LangCode[];
}

const value: I18nContextValue = {
  lang: 'en',
  setLang: () => {},
  t: (key, fallback) => CATALOG.en[key] ?? fallback ?? String(key),
  dir: 'ltr',
  labelFor: () => 'English',
  languages: ['en'],
};

const I18nContext = createContext<I18nContextValue>(value);

export function I18nProvider({ children }: { children: ReactNode }) {
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n(): I18nContextValue {
  return useContext(I18nContext);
}
