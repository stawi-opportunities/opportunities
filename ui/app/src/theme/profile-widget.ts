// Single source of truth for how the @stawi/profile widget looks
// inside opportunities.stawi.org. Site Nav (StawiAuth) mounts the avatar
// account control; design tokens + CSS live here only.
//
// Values mirror tailwind.config.js:
//   accent-600 #198535 / accent-500 #219c3f / accent-400 #45b739
//   navy-900   #0c1226

import type { ProfileWidgetTokens } from '@stawi/profile';

const SITE_FONT_STACK = `"Inter", system-ui, -apple-system, "Segoe UI", Roboto, sans-serif`;

export const profileWidgetTokens: ProfileWidgetTokens = {
  colorPrimary: '#219c3f',
  colorPrimaryHover: '#45b739',
  colorFocusRing: '#219c3f',
  radius: '8px',
  fontHeading: SITE_FONT_STACK,
  fontBody: SITE_FONT_STACK,
  fontWeightHeading: 600,
  // Circular account trigger — large enough to read the photo.
  triggerSize: '36px',
  avatarLargeSize: '72px',
};

// Shadow-DOM overrides: make the authenticated avatar clearly circular
// and the signed-out CTA compact for the site header.
export const profileWidgetCSS = `
  .aiw-signin-trigger {
    color: #0c1226;
    padding: 8px 16px;
    font-size: 14px;
    line-height: 1.25rem;
    letter-spacing: 0;
    border-radius: 8px;
    gap: 8px;
    box-shadow: 0 1px 2px rgba(12, 18, 38, 0.08);
  }
  .aiw-signin-trigger:hover {
    color: #0c1226;
    box-shadow: 0 2px 6px rgba(12, 18, 38, 0.14);
  }
  .aiw-signin-trigger:focus-visible {
    outline: 2px solid #219c3f;
    outline-offset: 2px;
  }
  .aiw-signin-avatar {
    width: 16px;
    height: 16px;
    color: inherit;
  }

  /* Authenticated account control — photo / initials circle */
  .aiw-trigger {
    width: var(--aiw-trigger-size, 36px);
    height: var(--aiw-trigger-size, 36px);
    min-width: var(--aiw-trigger-size, 36px);
    min-height: var(--aiw-trigger-size, 36px);
    border-radius: 9999px;
    overflow: hidden;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    padding: 0;
    border: 1.5px solid rgb(226 232 240);
    background: rgb(241 245 249);
    cursor: pointer;
  }
  .aiw-trigger:hover {
    border-color: #45b739;
    box-shadow: 0 0 0 3px rgb(33 156 63 / 0.15);
  }
  .aiw-trigger:focus-visible {
    outline: 2px solid #219c3f;
    outline-offset: 2px;
  }
  .aiw-trigger img,
  .aiw-trigger picture,
  .aiw-trigger [data-avatar],
  .aiw-avatar-overlay img {
    width: 100%;
    height: 100%;
    object-fit: cover;
    border-radius: 9999px;
    display: block;
  }
  .aiw-trigger-initials,
  .aiw-avatar-initials {
    width: 100%;
    height: 100%;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    font-size: 0.75rem;
    font-weight: 600;
    letter-spacing: 0.02em;
    color: #0c1226;
    background: linear-gradient(145deg, #bbf7d0, #86efac);
    border-radius: 9999px;
  }
`;
