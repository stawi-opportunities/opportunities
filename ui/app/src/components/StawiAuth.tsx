import { useEffect, useRef } from 'react';
import { mount, type MountHandle } from '@stawi/profile';
import { getConfig } from '@/utils/config';
import { authRuntime } from '@/auth/runtime';
import { profileWidgetTokens, profileWidgetCSS } from '@/theme/profile-widget';
import { useTheme } from '@/providers/ThemeProvider';

/**
 * Site-header account control via @stawi/profile.
 *
 * - Unauthenticated → Sign-in button
 * - Authenticated → circular avatar (picture / Gravatar / initials) + popover
 *
 * Profile is only mounted here (not in the dashboard drawer/sidebar).
 */
export function StawiAuth() {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const handleRef = useRef<MountHandle | null>(null);
  const { resolved: resolvedTheme } = useTheme();

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;

    // Remount when theme flips so tokens/CSS stay correct.
    handleRef.current?.unmount();
    handleRef.current = null;
    host.replaceChildren();

    const cfg = getConfig();
    try {
      handleRef.current = mount({
        target: host,
        runtime: authRuntime(),
        installationId: cfg.oidcInstallationID,
        clientId: cfg.oidcClientID,
        idpBaseUrl: cfg.oidcIssuer,
        apiBaseUrl: cfg.candidatesAPIURL,
        theme: resolvedTheme === 'dark' ? 'dark' : 'light',
        tokens: profileWidgetTokens,
        css: profileWidgetCSS,
        // Fall back to Gravatar when the profile has no uploaded photo.
        gravatar: true,
        onLogout: () => {
          window.location.href = '/';
        },
        onError: (err) => {
          console.error('[stawi/profile] onError:', err);
        },
      });
    } catch (err) {
      console.error('[stawi/profile] mount failed:', err);
    }

    return () => {
      handleRef.current?.unmount();
      handleRef.current = null;
    };
  }, [resolvedTheme]);

  return (
    <div
      className="relative flex min-h-9 min-w-9 items-center justify-end"
      aria-label="Account"
      role="region"
    >
      <div ref={hostRef} className="flex items-center" />
    </div>
  );
}
