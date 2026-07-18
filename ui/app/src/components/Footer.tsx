import { useI18n } from '@/i18n/I18nProvider';

export default function Footer() {
  const { t } = useI18n();
  const year = new Date().getFullYear();

  return (
    <footer className="mt-auto bg-navy-950 text-gray-400" role="contentinfo">
      <div className="mx-auto max-w-7xl px-4 py-14 sm:px-6 lg:px-8">
        <div className="grid grid-cols-2 gap-10 sm:grid-cols-4">
          <div className="col-span-2 sm:col-span-1">
            <a href="/" aria-label="Stawi">
              <img
                src="/images/logo-white.svg"
                alt="Stawi"
                height="32"
                className="h-8 w-auto opacity-90"
              />
            </a>
            <p className="mt-4 text-sm leading-relaxed text-gray-500">
              AI job matching — search free, get a real shortlist from your CV, subscribe only if
              you want more.
            </p>
          </div>
          <div>
            <h3 className="text-sm font-semibold uppercase tracking-wider text-white">
              {t('footer.explore')}
            </h3>
            <ul className="mt-4 space-y-2.5" role="list">
              <li>
                <a
                  href="/search/"
                  className="text-sm text-gray-400 transition-colors hover:text-white"
                >
                  {t('footer.findJobs')}
                </a>
              </li>
              <li>
                <a
                  href="/jobs/"
                  className="text-sm text-gray-400 transition-colors hover:text-white"
                >
                  {t('footer.jobs')}
                </a>
              </li>
              <li>
                <a
                  href="/categories/"
                  className="text-sm text-gray-400 transition-colors hover:text-white"
                >
                  {t('footer.categories')}
                </a>
              </li>
              <li>
                <a
                  href="/search/"
                  className="text-sm text-gray-400 transition-colors hover:text-white"
                >
                  {t('footer.advancedSearch')}
                </a>
              </li>
            </ul>
          </div>
          <div>
            <h3 className="text-sm font-semibold uppercase tracking-wider text-white">
              {t('footer.company')}
            </h3>
            <ul className="mt-4 space-y-2.5" role="list">
              <li>
                <a
                  href="/about/"
                  className="text-sm text-gray-400 transition-colors hover:text-white"
                >
                  {t('footer.about')}
                </a>
              </li>
              <li>
                <a
                  href="/faq/"
                  className="text-sm text-gray-400 transition-colors hover:text-white"
                >
                  {t('footer.faq')}
                </a>
              </li>
              <li>
                <a
                  href="/pricing/"
                  className="text-sm text-gray-400 transition-colors hover:text-white"
                >
                  {t('footer.pricing')}
                </a>
              </li>
              <li>
                <a
                  href="mailto:jobs@stawi.org"
                  className="text-sm text-gray-400 transition-colors hover:text-white"
                >
                  {t('footer.contact')}
                </a>
              </li>
            </ul>
          </div>
          <div>
            <h3 className="text-sm font-semibold uppercase tracking-wider text-white">
              {t('footer.legal')}
            </h3>
            <ul className="mt-4 space-y-2.5" role="list">
              <li>
                <a
                  href="/terms/"
                  className="text-sm text-gray-400 transition-colors hover:text-white"
                >
                  {t('footer.termsOfService')}
                </a>
              </li>
              <li>
                <a
                  href="/privacy/"
                  className="text-sm text-gray-400 transition-colors hover:text-white"
                >
                  {t('footer.privacyPolicy')}
                </a>
              </li>
            </ul>
          </div>
        </div>
        <div className="mt-12 flex flex-col items-start justify-between gap-2 border-t border-white/10 pt-6 sm:flex-row sm:items-center">
          <p className="text-sm text-gray-500">
            &copy; {year} Stawi Jobs. {t('footer.rights')}
          </p>
          <p className="text-sm text-gray-500">
            {t('footer.madeBy')}{' '}
            <a
              href="https://stawi.org"
              className="text-gray-400 transition-colors hover:text-white"
            >
              Stawi
            </a>
            .
          </p>
        </div>
      </div>
    </footer>
  );
}
