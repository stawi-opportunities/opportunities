import { useEffect, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import {
  deleteDefinition,
  getDefinition,
  putDefinition,
} from '@/api/admin-client';

// DefinitionEditor renders a textarea-driven editor for a single
// definition YAML. PUT broadcasts opportunities.definitions.changed.v1
// so every crawler/api/worker replica reloads the new body within
// seconds — operators can iterate on kind YAMLs, prompts, connector
// specs, and seeds directly in the browser without a redeploy.
export function DefinitionEditor() {
  const { type, name } = useParams<{ type: string; name: string }>();
  const navigate = useNavigate();
  const [body, setBody] = useState<string>('');
  const [original, setOriginal] = useState<string>('');
  const [status, setStatus] = useState<string | null>(null);
  const [loading, setLoading] = useState<boolean>(true);

  useEffect(() => {
    if (!type || !name) return;
    setLoading(true);
    setStatus(null);
    getDefinition(type, name)
      .then((b) => {
        setBody(b);
        setOriginal(b);
        setLoading(false);
      })
      .catch((e: unknown) => {
        setStatus(
          `Load failed: ${e instanceof Error ? e.message : String(e)}`
        );
        setLoading(false);
      });
  }, [type, name]);

  const dirty = body !== original;

  const save = async () => {
    if (!type || !name) return;
    setStatus('Saving…');
    try {
      await putDefinition(type, name, body);
      setOriginal(body);
      setStatus('Saved — broadcasting to crawlers.');
    } catch (e) {
      setStatus(
        `Save failed: ${e instanceof Error ? e.message : String(e)}`
      );
    }
  };

  const remove = async () => {
    if (!type || !name) return;
    if (!window.confirm(`Delete ${type}/${name}? This cannot be undone.`)) {
      return;
    }
    setStatus('Deleting…');
    try {
      await deleteDefinition(type, name);
      navigate('/definitions');
    } catch (e) {
      setStatus(
        `Delete failed: ${e instanceof Error ? e.message : String(e)}`
      );
    }
  };

  return (
    <div>
      <header>
        <h1>
          <Link to="/definitions">definitions</Link>
          {' / '}
          {type} / {name}
        </h1>
        {dirty && <small style={{ color: '#a60' }}>unsaved changes</small>}
      </header>

      {loading ? (
        <p>Loading…</p>
      ) : (
        <>
          <textarea
            value={body}
            onChange={(e) => setBody(e.target.value)}
            style={{
              width: '100%',
              height: '60vh',
              fontFamily:
                'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
              fontSize: '0.85rem',
              padding: '0.5rem',
              boxSizing: 'border-box',
            }}
            spellCheck={false}
          />
          <div
            style={{
              marginTop: '0.5rem',
              display: 'flex',
              gap: '0.5rem',
              alignItems: 'center',
            }}
          >
            <button type="button" onClick={save} disabled={!dirty}>
              Save
            </button>
            <button
              type="button"
              onClick={remove}
              style={{ color: 'crimson' }}
            >
              Delete
            </button>
            {status && <small>{status}</small>}
          </div>
        </>
      )}
    </div>
  );
}
