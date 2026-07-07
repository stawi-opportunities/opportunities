interface LoadingSkeletonProps {
  rows?: number;
  type?: "text" | "card" | "table-row";
}

export function LoadingSkeleton({
  rows = 3,
  type = "text",
}: LoadingSkeletonProps) {
  const inner = (() => {
    if (type === "card") {
      return (
        <div
          style={{
            background: "var(--c-surface)",
            border: "1px solid var(--c-border)",
            borderRadius: "var(--radius-lg)",
            padding: "1.25rem",
            animation: "fadeIn 0.3s ease-in",
          }}
        >
          <SkeletonLine width="50%" height="1.1rem" />
          <div style={{ height: "0.75rem" }} />
          <SkeletonLine width="90%" height="0.85rem" />
          <div style={{ height: "0.5rem" }} />
          <SkeletonLine width="70%" height="0.85rem" />
        </div>
      );
    }

    if (type === "table-row") {
      return (
        <>
          {Array.from({ length: rows }, (_, i) => (
            <div
              key={i}
              style={{
                display: "flex",
                gap: "0.75rem",
                padding: "0.55rem 0.6rem",
                borderBottom: "1px solid var(--c-border)",
              }}
            >
              <SkeletonLine width="25%" />
              <SkeletonLine width="15%" />
              <SkeletonLine width="20%" />
              <SkeletonLine width="15%" />
              <div style={{ flex: 1 }} />
            </div>
          ))}
        </>
      );
    }

    return (
      <div style={{ animation: "fadeIn 0.3s ease-in" }}>
        {Array.from({ length: rows }, (_, i) => (
          <div key={i} style={{ marginBottom: i < rows - 1 ? "0.6rem" : 0 }}>
            <SkeletonLine
              width={i === 0 ? "65%" : i === rows - 1 ? "40%" : "85%"}
            />
          </div>
        ))}
      </div>
    );
  })();

  return (
    <div aria-busy="true" role="status" aria-label="Loading">
      {inner}
    </div>
  );
}

function SkeletonLine({
  width,
  height = "0.85rem",
}: {
  width: string;
  height?: string;
}) {
  return (
    <div
      style={{
        width,
        height,
        borderRadius: "var(--radius-sm)",
        background: "var(--c-border)",
        animation: "skeleton-pulse 1.5s ease-in-out infinite",
      }}
    />
  );
}
