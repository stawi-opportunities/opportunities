import { useEffect } from 'react';
import { useAuth } from '@/providers/AuthProvider';

export default function SignupCta() {
  const { state } = useAuth();

  useEffect(() => {
    const ctaMount = document.getElementById('mount-get-started-cta');
    if (!ctaMount) return;
    ctaMount.style.display = state === 'authenticated' ? 'none' : '';
  }, [state]);

  // This island's entire job is DOM side-effects on the Hugo-rendered
  // block above; nothing new to render here.
  return null;
}
