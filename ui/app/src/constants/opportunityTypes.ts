import type { OpportunityKind } from '@/types/snapshot';
import type { IconName } from '@/components/ui/Icon';
import type { StringKey } from '@/i18n/strings';

export interface OpportunityTypeMeta {
  kind: OpportunityKind;
  iconName: IconName;
  emoji: string;
  href: string;
  comingSoon: boolean;
  labelKey: StringKey;
}

export const OPPORTUNITY_TYPE_META: OpportunityTypeMeta[] = [
  {
    kind: 'job',
    iconName: 'briefcase',
    emoji: '💼',
    href: '/jobs/',
    comingSoon: false,
    labelKey: 'kind.job',
  },
  {
    kind: 'scholarship',
    iconName: 'graduation',
    emoji: '🎓',
    href: '/scholarships/',
    comingSoon: true,
    labelKey: 'kind.scholarship',
  },
  {
    kind: 'tender',
    iconName: 'clipboard',
    emoji: '📋',
    href: '/tenders/',
    comingSoon: true,
    labelKey: 'kind.tender',
  },
  {
    kind: 'deal',
    iconName: 'tag',
    emoji: '🏷️',
    href: '/deals/',
    comingSoon: true,
    labelKey: 'kind.deal',
  },
  {
    kind: 'funding',
    iconName: 'money',
    emoji: '💰',
    href: '/funding/',
    comingSoon: true,
    labelKey: 'kind.funding',
  },
];

export function getTypeMeta(kind: string): OpportunityTypeMeta | undefined {
  return OPPORTUNITY_TYPE_META.find((t) => t.kind === kind);
}
