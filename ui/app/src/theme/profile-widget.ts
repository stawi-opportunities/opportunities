// Single source of truth for how the @stawi/profile widget looks
// inside opportunities.stawi.org. StawiAuth (nav button) and the Dashboard's
// profile popover both import from here so a tweak to the palette /
// radius / font-stack ripples everywhere the widget mounts.
//
// Visual language mirrors the hero "Get started" button on the
// home page (layouts/index.html): bg-accent-400 with navy-900 text,
// rounded-md, Inter semibold. Hover goes to accent-300 exactly
// like the hero.  WCAG contrast is comfortably AAA: navy-900 on
// accent-400 = 8.9:1, on accent-300 = 11.6:1.
//
// The widget renders into a shadow root, which means it cannot read
// the host page's CSS variables or Tailwind utilities — the override
// has to travel through mount({ tokens, css }). That trade-off
// forces us to duplicate the hex values here, but keeps the widget
// portable across Stawi apps that each have their own design
// language.
//
// Values mirror tailwind.config.js exactly:
//   accent-400 #45b739  (brand "leaf" green — Get started CTA)
//   accent-300 #86efac  (hover — same as hero button)
//   navy-900   #0c1226  (button text)
//   rounded-md 6px

import type { ProfileWidgetTokens } from "@stawi/profile";

const SITE_FONT_STACK =
  `"Inter", system-ui, -apple-system, "Segoe UI", Roboto, sans-serif`;

export const profileWidgetTokens: ProfileWidgetTokens = {
  colorPrimary:        "#45b739", // tw accent-400 (Get started green)
  colorPrimaryHover:   "#86efac", // tw accent-300 (lighter hover, matches hero)
  colorFocusRing:      "#45b739",
  radius:              "6px",     // tw rounded-md
  fontHeading:         SITE_FONT_STACK,
  fontBody:            SITE_FONT_STACK,
  fontWeightHeading:   600,       // tw font-semibold
};

// Shape + size overrides the tokens API doesn't cover. Navy-900 text
// goes here (the widget's default assumes white text on a dark
// primary; our primary is light-green so we flip to dark text).
// Button sized for a nav context — smaller than the hero's px-8 py-4
// so it doesn't dominate the header bar, but still visibly a CTA.
export const profileWidgetCSS = `
  .aiw-signin-trigger {
    color: #0c1226;
    padding: 10px 20px;
    font-size: 15px;
    line-height: 1.25rem;
    letter-spacing: 0;
    border-radius: 6px;
    gap: 8px;
    box-shadow: 0 1px 2px rgba(12, 18, 38, 0.08);
  }
  .aiw-signin-trigger:hover {
    color: #0c1226;
    box-shadow: 0 2px 6px rgba(12, 18, 38, 0.18);
  }
  .aiw-signin-trigger:focus-visible {
    outline: 2px solid #45b739;
    outline-offset: 2px;
  }
  .aiw-signin-avatar {
    width: 18px;
    height: 18px;
    color: inherit;
  }
`;
