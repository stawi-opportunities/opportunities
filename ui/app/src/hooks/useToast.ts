import { useToastStore, type ToastVariant } from '@/stores/toastStore';

/**
 * Push a toast notification from any component.
 *
 * ```tsx
 * const { push } = useToast();
 * push("Preferences saved.", "success");
 * push("Something went wrong.", "error");
 * ```
 *
 * Toasts auto-dismiss after 4 s. The visual container is rendered
 * exactly once per page by ToastProvider (first-mount-wins), so this
 * hook is safe to call from any island.
 */
export function useToast() {
  const push = useToastStore((s) => s.push);
  return { push } as {
    push: (message: string, variant?: ToastVariant) => void;
  };
}
