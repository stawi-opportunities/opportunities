export function fmtMoney(
  min: number | undefined,
  max: number | undefined,
  currency: string | undefined,
  period: string | undefined = "year",
): string {
  if (!min && !max) return "";
  const c = currency || "USD";
  const per = period ? `/${period}` : "";
  if (min && max && min !== max) {
    return `${c} ${min.toLocaleString()}–${max.toLocaleString()}${per}`;
  }
  return `${c} ${(max || min)!.toLocaleString()}${per}`;
}

export function isoInPast(iso?: string | null): boolean {
  if (!iso) return false;
  return new Date(iso).getTime() < Date.now();
}

/** Relative posted-at label: "3 days ago", "just now". */
export function timeAgo(iso?: string | null): string {
  if (!iso) return "";
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return "";
  const diff = Math.max(0, Date.now() - then);
  const mins = Math.floor(diff / 60_000);
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins} min ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(hrs / 24);
  if (days < 30) return `${days}d ago`;
  return new Date(iso).toLocaleDateString();
}
