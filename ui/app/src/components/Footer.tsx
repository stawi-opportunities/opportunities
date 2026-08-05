export default function Footer() {
  const year = new Date().getFullYear();

  return (
    <footer className="mt-auto border-t border-muted bg-surface" role="contentinfo">
      <div className="mx-auto flex max-w-7xl flex-wrap items-center justify-between gap-3 px-4 py-6 text-sm text-secondary sm:px-6 lg:px-8">
        <p>© {year} Stawi Jobs</p>
        <nav className="flex flex-wrap gap-4" aria-label="Footer">
          <a href="/pricing/" className="hover:text-main">
            Pricing
          </a>
          <a href="/terms/" className="hover:text-main">
            Terms
          </a>
          <a href="/privacy/" className="hover:text-main">
            Privacy
          </a>
        </nav>
      </div>
    </footer>
  );
}
