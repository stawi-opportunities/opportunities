import { useEffect } from 'react';
import { useAuth } from '@/providers/AuthProvider';

export default function SignupCta() {
  const { state } = useAuth();

  useEffect(() => {
    const ctaMount = document.getElementById('mount-get-started-cta');
    if (!ctaMount) return;
    ctaMount.style.display = state === 'authenticated' ? 'none' : '';
  }, [state]);

  return null;
}
