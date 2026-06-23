import { useCallback, useEffect, useRef, useState } from 'react';
import { useBlocker, useNavigate, useParams } from 'react-router-dom';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  deleteDefinition,
  getDefinition,
  putDefinition,
} from '@/api/admin-client';
import { Button, ConfirmDialog, ErrorBlock, LoadingSkeleton, useToast } from '@/components/ui';

export function DefinitionEditor() {
  const { type, name } = useParams<{ type: string; name: string }>();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { toast } = useToast();

  const [body, setBody] = useState('');
  const [showDelete, setShowDelete] = useState(false);
  const [showBlockedNav, setShowBlockedNav] = useState(false);
  const pendingNav = useRef<(() => void) | null>(null);

  const { data: original, isLoading, error } = useQuery({
    queryKey: ['definition', type, name],
    queryFn: () => getDefinition(type ?? '', name ?? ''),
    enabled: !!type && !!name,
  });

  useEffect(() => {
    if (original !== undefined) setBody(original);
  }, [original]);

  const dirty = original !== undefined && body !== original;

  useEffect(() => {
    if (!dirty) return;
    const handler = (e: BeforeUnloadEvent) => { e.preventDefault(); };
    window.addEventListener('beforeunload', handler);
    return () => window.removeEventListener('beforeunload', handler);
  }, [dirty]);

  const blocker = useBlocker(
    ({ currentLocation, nextLocation }) => dirty && currentLocation.pathname !== nextLocation.pathname
  );

  useEffect(() => {
    if (blocker.state === 'blocked') {
      setShowBlockedNav(true);
      pendingNav.current = blocker.proceed;
    }
  }, [blocker.state]);

  const save = useMutation({
    mutationFn: () => putDefinition(type ?? '', name ?? '', body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['definition', type, name] });
      queryClient.invalidateQueries({ queryKey: ['definitions'] });
      toast('Saved — broadcasting to crawlers.', { type: 'success' });
    },
    onError: (e: Error) => {
      toast(`Save failed: ${e.message}`, { type: 'error' });
    },
  });

  const remove = useMutation({
    mutationFn: () => deleteDefinition(type ?? '', name ?? ''),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['definitions'] });
      toast('Definition deleted.', { type: 'success' });
      navigate('/definitions');
    },
    onError: (e: Error) => {
      toast(`Delete failed: ${e.message}`, { type: 'error' });
      setShowDelete(false);
    },
  });

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 's') {
        e.preventDefault();
        if (dirty && !save.isPending) save.mutate();
      }
    },
    [dirty, save]
  );

  if (!type || !name) return <ErrorBlock message="Missing definition type or name" />;
  if (isLoading) return <LoadingSkeleton type="card" />;
  if (error) return <ErrorBlock message="Failed to load definition" detail={String(error)} />;

  return (
    <div>
      <div style={{ marginBottom: '0.75rem' }}>
        <h1 style={{ margin: 0, fontSize: '1.1rem', fontWeight: 600 }}>
          {type} / {name}
        </h1>
        {dirty && (
          <span
            style={{
              display: 'inline-block',
              marginTop: '0.25rem',
              fontSize: '0.78rem',
              color: 'var(--c-warning)',
              fontWeight: 500,
            }}
          >
            Unsaved changes
          </span>
        )}
      </div>

      <textarea
        value={body}
        onChange={(e) => setBody(e.target.value)}
        onKeyDown={handleKeyDown}
        spellCheck={false}
        aria-label="Definition editor"
        maxLength={100000}
        style={{
          width: '100%',
          height: '60vh',
          fontFamily: 'var(--font-mono)',
          fontSize: '0.85rem',
          padding: '0.75rem',
          boxSizing: 'border-box',
          border: `1px solid ${dirty ? 'var(--c-warning)' : 'var(--c-border)'}`,
          borderRadius: 'var(--radius-md)',
          outline: 'none',
          resize: 'vertical',
          tabSize: 2,
          lineHeight: 1.5,
        }}
      />

      <div
        style={{
          marginTop: '0.75rem',
          display: 'flex',
          gap: '0.5rem',
          alignItems: 'center',
        }}
      >
        <Button onClick={() => save.mutate()} disabled={!dirty} loading={save.isPending}>
          Save
        </Button>
        <Button variant="danger" onClick={() => setShowDelete(true)}>
          Delete
        </Button>
        {save.isSuccess && (
          <small style={{ color: 'var(--c-success)' }}>Saved</small>
        )}
      </div>

      <ConfirmDialog
        open={showDelete}
        title="Delete definition"
        message={`Delete ${type}/${name}? This cannot be undone.`}
        confirmLabel="Delete"
        variant="danger"
        busy={remove.isPending}
        onConfirm={() => remove.mutate()}
        onCancel={() => setShowDelete(false)}
      />

      <ConfirmDialog
        open={showBlockedNav}
        title="Unsaved changes"
        message="You have unsaved changes. Leave without saving?"
        confirmLabel="Leave"
        variant="danger"
        onConfirm={() => { setShowBlockedNav(false); pendingNav.current?.(); }}
        onCancel={() => { setShowBlockedNav(false); blocker.reset?.(); }}
      />
    </div>
  );
}
