import { StawiAuth } from './StawiAuth';

/** Site chrome: logo + auth only. In-product navigation lives in the dashboard. */
export default function Nav() {
  return (
    <header
      className="sticky top-0 z-40 border-b border-muted bg-nav-bg/95 backdrop-blur-xl"
      role="banner"
    >
      <div className="mx-auto flex h-14 max-w-7xl items-center justify-between gap-4 px-4 sm:px-6 lg:px-8">
        <a href="/" className="flex-shrink-0" aria-label="Stawi">
          <img src="/images/logo.svg" alt="Stawi" height="32" className="h-8 w-auto" />
        </a>
        <StawiAuth />
      </div>
    </header>
  );
}
