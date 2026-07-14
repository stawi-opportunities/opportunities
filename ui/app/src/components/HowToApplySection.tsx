import { useQuery } from '@tanstack/react-query';
import { fetchApplyDetails } from '@/api/candidates';
import { useAuth } from '@/providers/AuthProvider';
import { useSubscription } from '@/hooks/useSubscription';
import { useI18n } from '@/i18n/I18nProvider';

/**
 * Paywalled "How to apply" block.
 *
 * Public snapshots only expose `has_how_to_apply`. Subscribers fetch the
 * Markdown body via GET /matching/me/opportunities/{id}/apply; everyone
 * else sees a subscribe/login CTA so the instructions never ship in the
 * public HTML or JSON.
 */
export default function HowToApplySection({
  opportunityId,
  slug,
  hasHowToApply,
}: {
  opportunityId: string;
  slug: string;
  hasHowToApply?: boolean;
}) {
  const { t } = useI18n();
  const { state } = useAuth();
  const authed = state === 'authenticated';
  const sub = useSubscription();
  const active = sub.data?.status === 'active';

  const q = useQuery({
    queryKey: ['apply-details', opportunityId || slug],
    queryFn: () => fetchApplyDetails(opportunityId || slug),
    enabled: !!hasHowToApply && authed && active,
    staleTime: 5 * 60_000,
  });

  if (!hasHowToApply) return null;

  const body = q.data?.how_to_apply?.trim();
  const locked = !authed || !active || q.data?.locked;

  return (
    <section
      className="mt-10 rounded-lg border border-navy-100 bg-navy-50/40 p-5"
      aria-labelledby="how-to-apply-heading"
    >
      <h2 id="how-to-apply-heading" className="text-lg font-semibold text-gray-900">
        {t('job.howToApply')}
      </h2>

      {locked ? (
        <div className="mt-3 space-y-3">
          <p className="text-sm text-gray-600">{t('job.howToApplyLocked')}</p>
          <div className="flex flex-wrap gap-2">
            {!authed ? (
              <a href="/login/" className="btn-primary text-sm">
                {t('nav.signIn')}
              </a>
            ) : null}
            <a href="/pricing/" className="btn-primary text-sm">
              {t('cta.subscribe')}
            </a>
          </div>
        </div>
      ) : q.isLoading ? (
        <p className="mt-3 text-sm text-gray-500">{t('common.loading')}</p>
      ) : body ? (
        <div className="prose prose-slate mt-3 max-w-none whitespace-pre-line text-sm text-gray-800">
          {body}
        </div>
      ) : (
        <p className="mt-3 text-sm text-gray-600">{t('job.howToApplyEmpty')}</p>
      )}
    </section>
  );
}
