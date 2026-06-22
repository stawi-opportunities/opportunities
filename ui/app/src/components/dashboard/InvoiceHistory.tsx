import type { Invoice } from '@/api/billing';
import type { StringKey } from '@/i18n/strings';

const STATUS_LABEL: Record<string, StringKey> = {
  paid: 'dash.statusActive',
  pending: 'dash.paymentWaiting',
  failed: 'dash.statusPastDue',
};

const STATUS_BG: Record<string, string> = {
  paid: 'bg-emerald-50 text-emerald-700',
  pending: 'bg-amber-50 text-amber-700',
  failed: 'bg-red-50 text-red-700',
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
      <div className="rounded-lg border border-gray-200 bg-gray-50 p-6 text-center text-sm text-gray-500">
        {t('invoice.empty')}
      </div>
    );
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left text-sm">
        <thead>
          <tr className="border-b border-gray-200 text-xs font-medium uppercase tracking-wide text-gray-500">
            <th className="pb-2 pr-4">{t('invoice.date')}</th>
            <th className="pb-2 pr-4">{t('invoice.amount')}</th>
            <th className="pb-2 pr-4">{t('invoice.status')}</th>
            <th className="pb-2" />
          </tr>
        </thead>
        <tbody>
          {invoices.map((inv) => (
            <tr key={inv.id} className="border-b border-gray-100">
              <td className="py-2.5 pr-4 text-gray-900">
                {new Date(inv.date).toLocaleDateString()}
              </td>
              <td className="py-2.5 pr-4 font-medium text-gray-900">
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
  );
}
