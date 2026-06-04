import { useI18n } from '@/i18n/I18nProvider';

export default function Footer() {
  const { t } = useI18n();
  const year = new Date().getFullYear();

  return (
    <footer className="mt-auto border-t border-gray-200 bg-gray-50" role="contentinfo">
      <div className="mx-auto max-w-7xl px-6 py-14 sm:px-8 lg:px-12">
        <div className="grid grid-cols-2 gap-10">
          <div>
            <h3 className="text-base font-semibold text-navy-900">{t('footer.company')}</h3>
            <ul className="mt-4 space-y-3" role="list">
              <li>
                <a href="/about/" className="text-base text-gray-700 hover:text-navy-900">
                  {t('footer.about')}
                </a>
              </li>
              <li>
                <a href="/pricing/" className="text-base text-gray-700 hover:text-navy-900">
                  {t('footer.pricing')}
                </a>
              </li>
              <li>
                <a
                  href="mailto:jobs@stawi.org"
                  className="text-base text-gray-700 hover:text-navy-900"
                >
                  {t('footer.contact')}
                </a>
              </li>
            </ul>
          </div>
          <div>
            <h3 className="text-base font-semibold text-navy-900">{t('footer.legal')}</h3>
            <ul className="mt-4 space-y-3" role="list">
              <li>
                <a href="/terms/" className="text-base text-gray-700 hover:text-navy-900">
                  {t('footer.termsOfService')}
                </a>
              </li>
              <li>
                <a href="/privacy/" className="text-base text-gray-700 hover:text-navy-900">
                  {t('footer.privacyPolicy')}
                </a>
              </li>
            </ul>
          </div>
        </div>
        <div className="mt-12 flex flex-col items-start justify-between gap-2 border-t border-gray-200 pt-6 sm:flex-row sm:items-center">
          <p className="text-sm text-gray-600">
            &copy; {year} Stawi Jobs. {t('footer.rights')}
          </p>
          <p className="text-sm text-gray-500">
            {t('footer.madeBy')}{' '}
            <a href="https://stawi.org" className="hover:text-gray-700">
              Stawi
            </a>
            .
          </p>
        </div>
      </div>
    </footer>
  );
}
