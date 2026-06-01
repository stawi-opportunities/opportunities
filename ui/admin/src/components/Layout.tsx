import type { FormEvent } from 'react';
import { Link, Outlet, useNavigate } from 'react-router-dom';

// Layout renders the top nav + an <Outlet /> for the matched child route.
// Kept deliberately style-light so the styles in src/styles/admin.css
// (which already pads <body>) continue to handle horizontal margins.
// One quick-jump form lets operators paste a slug, a variant id, or a
// source id and land on the right trace page without leaving the page.
export function Layout() {
  const navigate = useNavigate();

  const goSlug = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const data = new FormData(e.currentTarget);
    const slug = String(data.get('slug') ?? '').trim();
    if (slug) navigate(`/opportunities/${encodeURIComponent(slug)}`);
  };

  const goVariant = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const data = new FormData(e.currentTarget);
    const id = String(data.get('variant') ?? '').trim();
    if (id) navigate(`/variants/${encodeURIComponent(id)}`);
  };

  return (
    <div>
      <nav
        style={{
          borderBottom: '1px solid #ddd',
          padding: '0.75rem 0',
          marginBottom: '1rem',
          display: 'flex',
          gap: '1rem',
          alignItems: 'center',
          flexWrap: 'wrap',
        }}
      >
        <Link to="/" style={{ fontWeight: 600 }}>
          Sources
        </Link>
        <Link to="/definitions">Definitions</Link>
        <form
          onSubmit={goSlug}
          style={{ marginLeft: 'auto', display: 'flex', gap: '0.25rem' }}
        >
          <input
            name="slug"
            placeholder="Opportunity slug…"
            style={{ padding: '0.25rem 0.4rem' }}
          />
          <button type="submit">Go</button>
        </form>
        <form onSubmit={goVariant} style={{ display: 'flex', gap: '0.25rem' }}>
          <input
            name="variant"
            placeholder="Variant id…"
            style={{ padding: '0.25rem 0.4rem' }}
          />
          <button type="submit">Go</button>
        </form>
      </nav>
      <main>
        <Outlet />
      </main>
    </div>
  );
}
