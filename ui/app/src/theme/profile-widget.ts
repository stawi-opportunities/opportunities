// Single source of truth for how the @stawi/profile widget looks
// inside stawi.jobs. StawiAuth (nav button) and the Dashboard's
// profile popover both import from here so a tweak to the palette /
// radius / font-stack ripples everywhere the widget mounts.
//
// The widget renders into a shadow root, which means it cannot read
// the host page's CSS variables or Tailwind utilities — the override
// has to travel through mount({ tokens, css }). That trade-off
// forces us to duplicate the hex values here, but keeps the widget
// portable across Stawi apps that each have their own design
// language.
//
// Values mirror tailwind.config.js exactly:
//   navy-900  #0c1226   (solid button background)
//   navy-800  #141a33   (hover)
//   rounded-md 6px      (matches pricing CTAs, 404 page, onboarding)

import type { ProfileWidgetTokens } from "@stawi/profile";

const SITE_FONT_STACK =
  `"Inter", system-ui, -apple-system, "Segoe UI", Roboto, sans-serif`;

export const profileWidgetTokens: ProfileWidgetTokens = {
  colorPrimary:        "#0c1226", // tw navy-900
  colorPrimaryHover:   "#141a33", // tw navy-800
  colorFocusRing:      "#0c1226",
  radius:              "6px",     // tw rounded-md
  fontHeading:         SITE_FONT_STACK,
  fontBody:            SITE_FONT_STACK,
  fontWeightHeading:   600,       // tw font-semibold
};

// Shape + size overrides the tokens API doesn't cover.  Matches the
// .rounded-md.bg-navy-900.px-5.py-2.5.text-sm.font-semibold pattern
// used across pricing / 404 / dashboard CTAs.
export const profileWidgetCSS = `
  .aiw-signin-trigger {
    padding: 10px 20px;
    font-size: 14px;
    line-height: 1.25rem;
    letter-spacing: 0;
    border-radius: 6px;
    gap: 6px;
    box-shadow: 0 1px 2px rgba(12, 18, 38, 0.08);
  }
  .aiw-signin-trigger:hover {
    box-shadow: 0 2px 6px rgba(12, 18, 38, 0.18);
  }
  .aiw-signin-avatar {
    width: 16px;
    height: 16px;
  }
`;
