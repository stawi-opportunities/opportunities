import { Link, useParams, useSearchParams } from "react-router-dom";
import { useMutation, useQuery } from "@tanstack/react-query";
import { getSeedDigest, reparseSource } from "@/api/admin-client";
import {
  Button,
  Card,
  ErrorBlock,
  LoadingSkeleton,
  useToast,
} from "@/components/ui";
import { RejectionChart } from "@/components/RejectionChart";

export function SeedDigest() {
  const { id } = useParams<{ id: string }>();
  const [params, setParams] = useSearchParams();
  const date = params.get("date") ?? new Date().toISOString().slice(0, 10);
  const { toast } = useToast();

  const { data, isLoading, error } = useQuery({
    queryKey: ["seed-digest", id, date],
    queryFn: () => getSeedDigest(id ?? "", date),
    enabled: !!id,
  });

  const reparse = useMutation({
    mutationFn: () => reparseSource(id ?? "", "24h"),
    onSuccess: (res) => {
      toast(
        `Queued ${res.queued} raw_payload(s) for re-extraction (window: ${res.window_seconds ?? "?"}s).`,
        {
          type: "success",
        },
      );
    },
    onError: (e: Error) => {
      toast(`Reparse failed: ${e.message}`, { type: "error" });
    },
  });

  if (!id) return <ErrorBlock message="Missing source ID" />;
  if (isLoading) return <LoadingSkeleton type="card" />;
  if (error)
    return (
      <ErrorBlock message="Failed to load digest" detail={String(error)} />
    );

  return (
    <div>
      <div style={{ marginBottom: "1.25rem" }}>
        <h1 style={{ margin: 0 }}>
          Digest{" "}
          <span
            style={{
              fontWeight: 400,
              fontSize: "0.9rem",
              color: "var(--c-text-secondary)",
            }}
          >
            for <Link to={`/sources/${encodeURIComponent(id)}`}>{id}</Link> on{" "}
            {date}
          </span>
        </h1>
        <div
          style={{
            display: "flex",
            gap: "0.75rem",
            alignItems: "center",
            marginTop: "0.5rem",
          }}
        >
          <label
            style={{
              fontSize: "0.88rem",
              display: "flex",
              alignItems: "center",
              gap: "0.35rem",
            }}
          >
            Date:
            <input
              type="date"
              value={date}
              onChange={(e) => setParams({ date: e.target.value })}
              style={{
                padding: "0.3rem 0.5rem",
                fontSize: "0.85rem",
                border: "1px solid var(--c-border)",
                borderRadius: "var(--radius-sm)",
              }}
            />
          </label>
          <Button
            variant="outline"
            size="sm"
            loading={reparse.isPending}
            onClick={() => reparse.mutate()}
          >
            Reparse last 24h
          </Button>
        </div>
      </div>

      {data && (
        <>
          <Card title="Counts" style={{ marginBottom: "1rem" }}>
            <table>
              <tbody>
                {data.crawl_jobs != null && (
                  <tr>
                    <td style={{ fontWeight: 500 }}>Crawl jobs</td>
                    <td>{data.crawl_jobs}</td>
                  </tr>
                )}
                <tr>
                  <td style={{ fontWeight: 500 }}>Variants emitted</td>
                  <td>{data.variants_emitted}</td>
                </tr>
                <tr>
                  <td style={{ fontWeight: 500 }}>Variants published</td>
                  <td>{data.variants_published}</td>
                </tr>
                <tr>
                  <td style={{ fontWeight: 500 }}>Variants rejected</td>
                  <td>{data.variants_rejected}</td>
                </tr>
              </tbody>
            </table>
            <p
              style={{
                margin: "0.5rem 0 0",
                fontSize: "0.8rem",
                color: "var(--c-text-secondary)",
              }}
            >
              data source: <code>{data.data_source}</code>
            </p>
          </Card>

          <Card title="Rejection reasons">
            <RejectionChart reasons={data.rejection_reasons ?? {}} />
          </Card>
        </>
      )}
    </div>
  );
}
