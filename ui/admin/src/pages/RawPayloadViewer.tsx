import { useParams } from 'react-router-dom';
import {
  getRawPayloadBodyURL,
  reparseRawPayload,
} from '@/api/admin-client';
import { useState } from 'react';

// RawPayloadViewer wraps /admin/raw_payloads/{id}/body in a sandboxed
// iframe so the operator can eyeball the captured HTML next to a
// re-extract button. The iframe uses sandbox="allow-same-origin" only —
// no script execution, no top-navigation — so a malicious captured
// page can't break out of the admin UI.
export function RawPayloadViewer() {
  const { id } = useParams<{ id: string }>();
  const [reparseMsg, setReparseMsg] = useState<string | null>(null);

  if (!id) return <p>No raw_payload id supplied.</p>;

  const handleReparse = async () => {
    setReparseMsg('Queuing…');
    try {
      const res = await reparseRawPayload(id);
      setReparseMsg(`Queued ${res.queued} raw_payload(s) for re-extraction.`);
    } catch (e) {
      setReparseMsg(
        `Reparse failed: ${e instanceof Error ? e.message : String(e)}`
      );
    }
  };

  return (
    <div>
      <header
        style={{
          display: 'flex',
          alignItems: 'baseline',
          gap: '1rem',
          flexWrap: 'wrap',
        }}
      >
        <h1 style={{ margin: 0 }}>
          Raw payload <code>{id}</code>
        </h1>
        <a href={getRawPayloadBodyURL(id)} target="_blank" rel="noreferrer">
          Open standalone →
        </a>
        <button type="button" onClick={handleReparse}>
          Reparse
        </button>
        {reparseMsg && <small>{reparseMsg}</small>}
      </header>
      <iframe
        title={`raw payload ${id}`}
        src={getRawPayloadBodyURL(id)}
        style={{
          marginTop: '1rem',
          width: '100%',
          height: 'calc(100vh - 220px)',
          border: '1px solid #ccc',
        }}
        sandbox="allow-same-origin"
      />
    </div>
  );
}
