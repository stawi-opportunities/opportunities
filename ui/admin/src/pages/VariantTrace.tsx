import { useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import {
  getVariantTrace,
  type VariantTimelineResponse,
} from "@/api/admin-client";
import { TraceTimeline } from "@/components/TraceTimeline";

// VariantTrace renders GET /admin/trace/variants/{id}: full join across
// source, crawl job, and stage transitions for one parsed variant.
export function VariantTrace() {
  const { id } = useParams<{ id: string }>();
  const [data, setData] = useState<VariantTimelineResponse | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    if (!id) return;
    setData(null);
    setErr(null);
    getVariantTrace(id)
      .then(setData)
      .catch((e: unknown) =>
        setErr(e instanceof Error ? e.message : String(e)),
      );
  }, [id]);

  if (err) return <pre style={{ color: "crimson" }}>{err}</pre>;
  if (!data) return <p>Loading variant…</p>;

  return (
    <div>
      <header>
        <h1>
          Variant <code>{data.variant_id}</code>
        </h1>
        <p>
          <strong>Source:</strong>{" "}
          <Link to={`/sources/${encodeURIComponent(data.source.id)}`}>
            {data.source.id}
          </Link>{" "}
          ({data.source.type})
          <br />
          <strong>Current stage:</strong> <code>{data.current_stage}</code>
          {data.opportunity_slug && (
            <>
              <br />
              <strong>Opportunity:</strong>{" "}
              <Link
                to={`/opportunities/${encodeURIComponent(data.opportunity_slug)}`}
              >
                {data.opportunity_slug}
              </Link>
            </>
          )}
        </p>
      </header>

      <section>
        <h2>Timeline</h2>
        <TraceTimeline stages={data.stages} />
      </section>

      {data.last_error && (
        <section>
          <h2>Last error</h2>
          <pre
            style={{
              color: "crimson",
              background: "#fee",
              padding: "0.5rem",
              whiteSpace: "pre-wrap",
            }}
          >
            {data.last_error}
          </pre>
        </section>
      )}
    </div>
  );
}
