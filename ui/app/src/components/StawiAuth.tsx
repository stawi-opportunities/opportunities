import { useEffect, useRef } from 'react';
import { mount, type MountHandle } from '@stawi/profile';
import { getConfig } from '@/utils/config';
import { authRuntime } from '@/auth/runtime';
import { profileWidgetTokens, profileWidgetCSS } from '@/theme/profile-widget';

/**
 * Mounts @stawi/profile (≥1.3.4) with stawi.opportunities visual tokens.
 *
 * The widget owns an auth display FSM (no host-side chrome branching):
 *   initializing          → nothing (avoids Sign-in flash during restore)
 *   authenticated|refreshing → avatar + profile popover
 *   unauthenticated|error → Sign-in button
 *
 * We pass the shared auth runtime singleton so the widget's token store
 * and our API client stay in sync.
 */
export function StawiAuth() {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const handleRef = useRef<MountHandle | null>(null);

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    if ((host.shadowRoot || host.childElementCount > 0) && handleRef.current) return;

    const cfg = getConfig();
    try {
      handleRef.current = mount({
        target: host,
        runtime: authRuntime(),
        installationId: cfg.oidcInstallationID,
        clientId: cfg.oidcClientID,
        idpBaseUrl: cfg.oidcIssuer,
        apiBaseUrl: cfg.candidatesAPIURL,
        theme: 'light',
        tokens: profileWidgetTokens,
        css: profileWidgetCSS,
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
  }, []);

  // Host is intentionally empty-sized: while auth is initializing the
  // widget renders nothing, so we must not reserve a Sign-in-shaped slot.
  return (
    <div className="relative flex items-center" aria-label="Account" role="region">
      <div ref={hostRef} />
    </div>
  );
}
