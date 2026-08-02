import type { SectionId } from './DashboardSidebar';
import type { StringKey } from '@/i18n/strings';

const SECTION_LABELS: Record<SectionId, StringKey> = {
  overview: 'nav.overview',
  feed: 'nav.feed',
  matches: 'nav.matches',
  tools: 'nav.tools',
  saved: 'nav.saved',
  applications: 'nav.applications',
  preferences: 'nav.preferences',
  billing: 'nav.billing',
  settings: 'nav.settings',
};

export function DashboardBreadcrumbs({
  active,
  t,
}: {
  active: SectionId;
  t: (k: StringKey, fallback?: string) => string;
}) {
  const crumbs = [
    { href: '/', label: t('common.home') },
    { href: '/dashboard/', label: t('dash.breadcrumbDashboard') },
  ];

  if (active !== 'overview') {
    crumbs.push({ href: `#${active}`, label: t(SECTION_LABELS[active]) });
  }

  return (
    <nav aria-label="Breadcrumb" className="mb-4">
      <ol role="list" className="flex flex-wrap items-center gap-1.5 text-sm">
        {crumbs.map((crumb, i) => {
          const isLast = i === crumbs.length - 1;
          return (
            <li key={crumb.href} className="flex items-center gap-1.5">
              {i > 0 && (
                <svg
                  className="h-3.5 w-3.5 flex-shrink-0 text-muted-strong"
                  fill="none"
                  viewBox="0 0 24 24"
                  strokeWidth={2}
                  stroke="currentColor"
                >
                  <path strokeLinecap="round" strokeLinejoin="round" d="m9 20.247 6-16.5" />
                </svg>
              )}
              {isLast ? (
                <span className="font-medium text-main" aria-current="page">
                  {crumb.label}
                </span>
              ) : (
                <a href={crumb.href} className="text-secondary hover:text-main">
                  {crumb.label}
                </a>
              )}
            </li>
          );
        })}
      </ol>
    </nav>
  );
}
