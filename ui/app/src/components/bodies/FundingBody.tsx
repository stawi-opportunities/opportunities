import { fundingFocusArea, type OpportunitySnapshot } from '@/types/snapshot';
import { fmtMoney } from '@/utils/format';
import { useI18n } from '@/i18n/I18nProvider';
import DescriptionBody from '@/components/common/DescriptionBody';

export default function FundingBody({ snap }: { snap: OpportunitySnapshot }) {
  const { t } = useI18n();
  const focus = fundingFocusArea(snap);
  const orgEligibility = stringAttr(snap, 'organisation_eligibility');
  const targetRegions = stringArrayAttr(snap, 'target_regions');
  const grant = fmtMoney(snap.amount_min, snap.amount_max, snap.currency, '');

  return (
    <>
      <div className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-gray-600">
        {focus && (
          <span className="rounded-full bg-gray-100 px-2 py-0.5 text-xs capitalize">{focus}</span>
        )}
        {grant && (
          <span className="font-medium text-emerald-700">
            {t('funding.grant')}: {grant}
          </span>
        )}
      </div>

      <dl className="mt-6 grid grid-cols-1 gap-4 sm:grid-cols-2">
        {orgEligibility && <Field label={t('funding.orgEligibility')} value={orgEligibility} />}
        {targetRegions.length > 0 && (
          <Field label={t('funding.targetRegions')} value={targetRegions.join(', ')} />
        )}
      </dl>

      <DescriptionBody
        html={snap.description_html ?? snap.description}
        ariaLabel="Funding description"
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

function stringArrayAttr(snap: OpportunitySnapshot, key: string): string[] {
  const v = snap.attributes?.[key];
  if (!Array.isArray(v)) return [];
  return v.filter((x): x is string => typeof x === 'string');
}
