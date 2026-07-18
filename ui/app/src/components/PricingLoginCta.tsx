import { useEffect } from 'react';
import { useAuth } from '@/providers/AuthProvider';
import { startLogin } from '@/auth/startLogin';

/**
 * Pricing plan buttons: start OIDC from the plan URL so returnTo keeps
 * ?plan=… (post-login → /onboarding/?plan=… for unpaid users).
 */
export default function PricingLoginCta() {
  const { hasSession, login } = useAuth();

  useEffect(() => {
    if (hasSession) return;
    const links = document.querySelectorAll<HTMLAnchorElement>('a[data-login-cta]');
    if (!links.length) return;

    const handlers: Array<{ el: HTMLAnchorElement; fn: (e: MouseEvent) => void }> = [];

    links.forEach((el) => {
      const fn = (e: MouseEvent) => {
        e.preventDefault();
        const href = el.getAttribute('href') || '/onboarding/';
        // Ensure returnTo includes the plan query (auth runtime uses current URL).
        if (window.location.pathname + window.location.search !== href) {
          window.history.replaceState({}, '', href);
        }
        void startLogin(login);
      };
      el.addEventListener('click', fn);
      handlers.push({ el, fn });
    });

    return () => {
      handlers.forEach(({ el, fn }) => el.removeEventListener('click', fn));
    };
  }, [hasSession, login]);

  return null;
}
