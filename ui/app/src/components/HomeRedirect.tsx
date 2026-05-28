import { useEffect } from 'react';
import { useAuth } from '@/providers/AuthProvider';

export default function HomeRedirect() {
  const { state } = useAuth();

  useEffect(() => {
    if (state === 'authenticated') {
      window.location.replace('/dashboard/');
    }
  }, [state]);

  return null;
}
