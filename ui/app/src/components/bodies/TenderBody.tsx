import { tenderProcurementDomain, type OpportunitySnapshot } from '@/types/snapshot';
import { fmtMoney } from '@/utils/format';
import { useI18n } from '@/i18n/I18nProvider';
import DescriptionBody from '@/components/common/DescriptionBody';

export default function TenderBody({ snap }: { snap: OpportunitySnapshot }) {
  const { t } = useI18n();
  const domain = tenderProcurementDomain(snap);
  const bidderEligibility = stringAttr(snap, 'bidder_eligibility');
  const submissionMethod = stringAttr(snap, 'submission_method');
  const budget = fmtMoney(snap.amount_min, snap.amount_max, snap.currency, '');

  return (
    <>
      <div className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-gray-600">
        {domain && (
          <span className="rounded-full bg-gray-100 px-2 py-0.5 text-xs capitalize">{domain}</span>
        )}
        {budget && (
          <span className="font-medium text-emerald-700">
            {t('tender.budget')}: {budget}
          </span>
        )}
      </div>

      <dl className="mt-6 grid grid-cols-1 gap-4 sm:grid-cols-2">
        {bidderEligibility && (
          <Field label={t('tender.bidderEligibility')} value={bidderEligibility} />
        )}
        {submissionMethod && (
          <Field label={t('tender.submissionMethod')} value={submissionMethod} />
        )}
      </dl>

      <DescriptionBody
        html={snap.description_html ?? snap.description}
        ariaLabel="Tender description"
      />
    </>
  );
}

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="text-xs font-medium uppercase tracking-wide text-gray-500">{label}</dt>
      <dd className="mt-1 text-sm text-gray-900">{value}</dd>
    </div>
  );
}

function stringAttr(snap: OpportunitySnapshot, key: string): string | undefined {
  const v = snap.attributes?.[key];
  return typeof v === 'string' ? v : undefined;
}
