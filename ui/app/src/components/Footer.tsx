export default function Footer() {
  const year = new Date().getFullYear();

  return (
    <footer className="mt-auto border-t border-gray-100 bg-white" role="contentinfo">
      <div className="mx-auto flex max-w-7xl flex-wrap items-center justify-between gap-3 px-4 py-6 text-sm text-gray-500 sm:px-6 lg:px-8">
        <p>© {year} Stawi Jobs</p>
        <nav className="flex flex-wrap gap-4" aria-label="Footer">
          <a href="/pricing/" className="hover:text-gray-800">
            Pricing
          </a>
          <a href="/terms/" className="hover:text-gray-800">
            Terms
          </a>
          <a href="/privacy/" className="hover:text-gray-800">
            Privacy
          </a>
        </nav>
      </div>
    </footer>
  );
}
