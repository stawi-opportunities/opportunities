import { StawiAuth } from "./StawiAuth";
import { LanguageSwitcher } from "./LanguageSwitcher";
import { useI18n } from "@/i18n/I18nProvider";

/**
 * Top-level navigation bar — intentionally minimal. One link to
 * the advanced search ("Jobs"), the language switcher, and the
 * auth widget. The earlier "Find Jobs" drop-down mixed the latest
 * feed, the search page, and a category listing; collapsing to a
 * single Jobs → /search/ link keeps the header from looking like a
 * desktop dashboard on small screens, and /search/ already exposes
 * categories via the filter sidebar.
 */
export default function Nav() {
  const { t } = useI18n();

  return (
    <header className="sticky top-0 z-40 border-b border-gray-200 bg-white" role="banner">
      <nav
        className="mx-auto flex h-28 max-w-7xl items-center justify-between gap-6 px-6 sm:px-8 lg:px-12"
        aria-label="Main navigation"
      >
        <a
          href="/"
          className="flex items-center"
          aria-label="Stawi — Growing together"
        >
          <img src="/images/logo.svg" alt="Stawi — Growing together" height="56" className="h-14 w-auto" />
        </a>

        <a
          href="/search/"
          className="text-lg font-semibold text-navy-900 hover:text-accent-600"
        >
          {t("nav.jobs")}
        </a>

        <div className="flex items-center gap-3">
          <LanguageSwitcher />
          <StawiAuth />
        </div>
      </nav>
    </header>
  );
}
