import { useEffect, useRef, type RefObject } from 'react';

const FOCUSABLE = 'a[href], button:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])';

export function useFocusTrap(
  ref: RefObject<HTMLElement | null>,
  active: boolean,
  onClose?: () => void
) {
  const prevRef = useRef<Element | null>(null);

  useEffect(() => {
    if (!active || !ref.current) return;

    prevRef.current = document.activeElement;

    const el = ref.current;
    const focusables = () => {
      const nodes = el.querySelectorAll<HTMLElement>(FOCUSABLE);
      return nodes.length > 0 ? nodes : null;
    };

    const first = focusables();
    first?.[0]?.focus();

    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        onClose?.();
        return;
      }
      if (e.key !== 'Tab') return;
      const f = focusables();
      if (!f || f.length === 0) {
        e.preventDefault();
        return;
      }
      const firstEl = f[0]!;
      const lastEl = f[f.length - 1]!;
      if (e.shiftKey) {
        if (document.activeElement === firstEl) {
          e.preventDefault();
          lastEl.focus();
        }
      } else {
        if (document.activeElement === lastEl) {
          e.preventDefault();
          firstEl.focus();
        }
      }
    };

    document.addEventListener('keydown', onKeyDown);

    return () => {
      document.removeEventListener('keydown', onKeyDown);
      (prevRef.current as HTMLElement)?.focus?.();
    };
  }, [active, ref, onClose]);
}
