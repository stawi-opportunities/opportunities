import { useParams } from 'react-router-dom';
import { useMutation } from '@tanstack/react-query';
import { getRawPayloadBodyURL, reparseRawPayload } from '@/api/admin-client';
import { Button, ErrorBlock, useToast } from '@/components/ui';

export function RawPayloadViewer() {
  const { id } = useParams<{ id: string }>();
  const { toast } = useToast();

  const reparse = useMutation({
    mutationFn: () => reparseRawPayload(id ?? ''),
    onSuccess: (res) => {
      toast(`Queued ${res.queued} raw_payload(s) for re-extraction.`, { type: 'success' });
    },
    onError: (e: Error) => {
      toast(`Reparse failed: ${e.message}`, { type: 'error' });
    },
  });

  if (!id) return <ErrorBlock message="No raw_payload id supplied." />;

  return (
    <div>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: '1rem',
          flexWrap: 'wrap',
          marginBottom: '1rem',
        }}
      >
        <h1 style={{ margin: 0, fontSize: '1.15rem' }}>
          Raw payload <code>{id}</code>
        </h1>
        <a href={getRawPayloadBodyURL(id)} target="_blank" rel="noreferrer" style={{ fontSize: '0.88rem' }}>
          Open standalone →
        </a>
        <Button size="sm" variant="outline" loading={reparse.isPending} onClick={() => reparse.mutate()}>
          Reparse
        </Button>
      </div>
      <iframe
        title={`raw payload ${id}`}
        src={getRawPayloadBodyURL(id)}
        style={{
          width: '100%',
          height: 'calc(100vh - 220px)',
          border: '1px solid var(--c-border)',
          borderRadius: 'var(--radius-md)',
        }}
        sandbox="allow-same-origin"
      />
    </div>
  );
}
