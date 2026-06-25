import { Link, useParams } from 'react-router-dom';
import { useMutation, useQuery } from '@tanstack/react-query';
import {
  getVariantTrace,
  reparseRawPayload,
} from '@/api/admin-client';
import { TraceTimeline } from '@/components/TraceTimeline';
import { Button, Card, ErrorBlock, LoadingSkeleton, useToast } from '@/components/ui';

export function VariantTrace() {
  const { id } = useParams<{ id: string }>();
  const { toast } = useToast();

  const { data, isLoading, error } = useQuery({
    queryKey: ['variant-trace', id],
    queryFn: () => getVariantTrace(id ?? ''),
    enabled: !!id,
  });

  const reparse = useMutation({
    mutationFn: () => reparseRawPayload(data?.raw_payload?.id ?? ''),
    onSuccess: (res) => {
      toast(`Queued ${res.queued} raw_payload(s) for re-extraction.`, { type: 'success' });
    },
    onError: (e: Error) => {
      toast(`Reparse failed: ${e.message}`, { type: 'error' });
    },
  });

  if (!id) return <ErrorBlock message="Missing variant ID" />;
  if (isLoading) return <LoadingSkeleton type="card" />;
  if (error) return <ErrorBlock message="Failed to load variant" detail={String(error)} />;
  if (!data) return null;

  return (
    <div>
      <div style={{ marginBottom: '1.25rem' }}>
        <h1 style={{ margin: 0, fontSize: '1.15rem' }}>
          Variant <code>{data.variant_id}</code>
        </h1>
        <div style={{ marginTop: '0.35rem', fontSize: '0.88rem', color: 'var(--c-text-secondary)' }}>
          <strong>Source:</strong>{' '}
          <Link to={`/sources/${encodeURIComponent(data.source.id)}`}>
            {data.source.id}
          </Link>{' '}
          ({data.source.type})
          <br />
          <strong>Current stage:</strong> <code>{data.current_stage}</code>
          {data.opportunity_slug && (
            <>
              <br />
              <strong>Opportunity:</strong>{' '}
              <Link to={`/opportunities/${encodeURIComponent(data.opportunity_slug)}`}>
                {data.opportunity_slug}
              </Link>
            </>
          )}
        </div>
      </div>

      <Card title="Timeline" style={{ marginBottom: '1rem' }}>
        <TraceTimeline stages={data.stages} />
      </Card>

      {data.raw_payload && (
        <Card title="Raw payload" style={{ marginBottom: '1rem' }}>
          <table>
            <tbody>
              <tr>
                <td style={{ fontWeight: 500 }}>ID</td>
                <td>
                  <Link to={`/raw_payloads/${encodeURIComponent(data.raw_payload.id)}`}>
                    {data.raw_payload.id}
                  </Link>
                </td>
              </tr>
              {data.raw_payload.source_url && (
                <tr>
                  <td style={{ fontWeight: 500 }}>Source URL</td>
                  <td>
                    <a href={data.raw_payload.source_url} target="_blank" rel="noreferrer">
                      {data.raw_payload.source_url}
                    </a>
                  </td>
                </tr>
              )}
              {data.raw_payload.content_hash && (
                <tr>
                  <td style={{ fontWeight: 500 }}>Content hash</td>
                  <td><code>{data.raw_payload.content_hash}</code></td>
                </tr>
              )}
              <tr>
                <td style={{ fontWeight: 500 }}>Size</td>
                <td>{data.raw_payload.size_bytes} B</td>
              </tr>
              <tr>
                <td style={{ fontWeight: 500 }}>HTTP status</td>
                <td>{data.raw_payload.http_status}</td>
              </tr>
              <tr>
                <td style={{ fontWeight: 500 }}>Fetched at</td>
                <td>{new Date(data.raw_payload.fetched_at).toLocaleString()}</td>
              </tr>
            </tbody>
          </table>
          <div style={{ marginTop: '0.75rem', display: 'flex', gap: '0.75rem', alignItems: 'center' }}>
            {data.raw_payload.body_url && (
              <a href={data.raw_payload.body_url} target="_blank" rel="noreferrer">
                View captured HTML →
              </a>
            )}
            <Button size="sm" variant="outline" loading={reparse.isPending} onClick={() => reparse.mutate()}>
              Reparse
            </Button>
          </div>
        </Card>
      )}

      {data.last_error && (
        <Card title="Last error">
          <pre
            style={{
              color: 'var(--c-danger)',
              background: '#fef2f2',
              padding: '0.75rem',
              borderRadius: 'var(--radius-sm)',
              whiteSpace: 'pre-wrap',
              fontSize: '0.85rem',
              margin: 0,
              overflowX: 'auto',
            }}
          >
            {data.last_error}
          </pre>
        </Card>
      )}
    </div>
  );
}
