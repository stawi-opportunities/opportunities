import type { Invoice } from '@/api/billing';
import type { StringKey } from '@/i18n/strings';

const STATUS_LABEL: Record<string, StringKey> = {
  paid: 'dash.statusActive',
  pending: 'dash.paymentWaiting',
  failed: 'dash.statusPastDue',
};

const STATUS_BG: Record<string, string> = {
  paid: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300',
  pending: 'bg-amber-50 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300',
  failed: 'bg-red-50 text-red-700 dark:bg-red-900/30 dark:text-red-300',
};

export function InvoiceHistory({
  invoices,
  t,
}: {
  invoices: Invoice[];
  t: (k: StringKey, fallback?: string) => string;
}) {
  if (!invoices.length) {
    return (
      <div className="rounded-lg border border-gray-200 bg-gray-50 p-6 text-center text-sm text-gray-500 dark:border-navy-700 dark:bg-navy-800 dark:text-gray-400">
        {t('invoice.empty')}
      </div>
    );
  }

  return (
    <>
      {/* Desktop table */}
      <div className="hidden sm:block overflow-x-auto">
        <table className="w-full text-left text-sm">
          <thead>
            <tr className="border-b border-gray-200 text-xs font-medium uppercase tracking-wide text-gray-500 dark:border-navy-700 dark:text-gray-400">
              <th className="pb-2 pr-4">{t('invoice.date')}</th>
              <th className="pb-2 pr-4">{t('invoice.amount')}</th>
              <th className="pb-2 pr-4">{t('invoice.status')}</th>
              <th className="pb-2" />
            </tr>
          </thead>
          <tbody>
            {invoices.map((inv) => (
              <tr key={inv.id} className="border-b border-gray-100 dark:border-navy-700">
                <td className="py-2.5 pr-4 text-gray-900 dark:text-white">
                  {new Date(inv.date).toLocaleDateString()}
                </td>
                <td className="py-2.5 pr-4 font-medium text-gray-900 dark:text-white">
                  {inv.currency} {inv.amount.toFixed(2)}
                </td>
                <td className="py-2.5 pr-4">
                  <span
                    className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${STATUS_BG[inv.status] ?? 'bg-gray-100 text-gray-600'}`}
                  >
                    {t(STATUS_LABEL[inv.status] ?? 'common.loading')}
                  </span>
                </td>
                <td className="py-2.5">
                  {inv.pdf_url ? (
                    <a
                      href={inv.pdf_url}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="text-sm font-medium text-accent-600 hover:text-accent-700"
                    >
                      {t('invoice.download')}
                    </a>
                  ) : null}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {/* Mobile cards */}
      <div className="space-y-3 sm:hidden">
        {invoices.map((inv) => (
          <div
            key={inv.id}
            className="rounded-lg border border-gray-200 bg-white p-4 dark:border-navy-700 dark:bg-navy-900"
          >
            <div className="flex items-center justify-between">
              <span className="text-sm text-gray-900 dark:text-white">
                {new Date(inv.date).toLocaleDateString()}
              </span>
              <span
                className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${STATUS_BG[inv.status] ?? 'bg-gray-100 text-gray-600'}`}
              >
                {t(STATUS_LABEL[inv.status] ?? 'common.loading')}
              </span>
            </div>
            <div className="mt-2 flex items-center justify-between">
              <span className="font-medium text-gray-900 dark:text-white">
                {inv.currency} {inv.amount.toFixed(2)}
              </span>
              {inv.pdf_url ? (
                <a
                  href={inv.pdf_url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-sm font-medium text-accent-600 hover:text-accent-700"
                >
                  {t('invoice.download')}
                </a>
              ) : null}
            </div>
          </div>
        ))}
      </div>
    </>
  );
}
