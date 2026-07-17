import { applyToOpportunity } from '@/api/candidates';

/**
 * Open the employer apply URL, then track the application on our side.
 * Never claims success without opening an external page when a URL exists.
 */
export async function openApplyAndTrack(
  opportunityId: string,
  applyURL: string | undefined | null,
  opts?: {
    onTracked?: () => void;
    onTrackFailed?: () => void;
    toast?: (msg: string, kind: 'success' | 'error' | 'info') => void;
  }
): Promise<'opened' | 'no_url'> {
  const url = applyURL?.trim();
  if (!url) {
    opts?.toast?.('No apply link for this role yet — open the job detail page.', 'info');
    return 'no_url';
  }
  window.open(url, '_blank', 'noopener,noreferrer');
  try {
    await applyToOpportunity(opportunityId, 'manual');
    opts?.onTracked?.();
    opts?.toast?.('Opened apply page — tracked as applied. Good luck!', 'success');
  } catch {
    opts?.onTrackFailed?.();
    opts?.toast?.(
      'Opened apply page, but we could not track it. You can still apply on the employer site.',
      'info'
    );
  }
  return 'opened';
}
