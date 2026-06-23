import { useEffect, useRef } from 'react';
import { createPortal } from 'react-dom';

const MAIN_ID = 'main-content';

export default function SkipToContent() {
  const ref = useRef<HTMLAnchorElement>(null);

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Tab' && !e.shiftKey && document.activeElement === ref.current) {
        const main = document.getElementById(MAIN_ID);
        if (main) {
          e.preventDefault();
          main.setAttribute('tabindex', '-1');
          main.focus();
          main.addEventListener('blur', () => main.removeAttribute('tabindex'), { once: true });
        }
      }
    };
    document.addEventListener('keydown', handler);
    return () => document.removeEventListener('keydown', handler);
  }, []);

  return createPortal(
    <a
      ref={ref}
      href={`#${MAIN_ID}`}
      className="fixed -top-40 left-4 z-[9999] rounded bg-white px-4 py-2 text-sm font-medium text-navy-700 shadow-lg transition-all duration-200 focus:top-4 focus:outline-none focus:ring-2 focus:ring-navy-500"
      onClick={(e) => {
        e.preventDefault();
        const main = document.getElementById(MAIN_ID);
        if (main) {
          main.setAttribute('tabindex', '-1');
          main.focus();
          main.addEventListener('blur', () => main.removeAttribute('tabindex'), { once: true });
        }
      }}
    >
      Skip to content
    </a>,
    document.body
  );
}
