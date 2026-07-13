import { useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useMutation } from "@tanstack/react-query";
import {
  createSource,
  SOURCE_TYPES,
  STOCK_RECIPES,
  type CreateSourceRequest,
} from "@/api/admin-client";
import {
  Button,
  Card,
  ErrorBlock,
  useToast,
} from "@/components/ui";

const fieldStyle: React.CSSProperties = {
  width: "100%",
  padding: "0.45rem 0.6rem",
  border: "1px solid var(--c-border)",
  borderRadius: "var(--radius-sm)",
  fontSize: "0.9rem",
  boxSizing: "border-box",
};

const labelStyle: React.CSSProperties = {
  display: "block",
  fontSize: "0.8rem",
  fontWeight: 600,
  marginBottom: "0.3rem",
  color: "var(--c-text-secondary)",
};

export function SourceCreate() {
  const navigate = useNavigate();
  const { toast } = useToast();
  const [form, setForm] = useState<CreateSourceRequest>({
    type: "api",
    base_url: "",
    name: "",
    country: "",
    language: "en",
    kinds: ["job"],
    crawl_interval_sec: 43200,
    listing_path: "",
    auto_approve: false,
  });
  const [kindsText, setKindsText] = useState("job");
  const [stockRecipe, setStockRecipe] = useState("");

  const mut = useMutation({
    mutationFn: createSource,
    onSuccess: (src) => {
      toast("Source created", { type: "success" });
      navigate(`/sources/${encodeURIComponent(src.id)}`);
    },
    onError: (e: Error) => toast(e.message, { type: "error" }),
  });

  const onSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (!form.base_url.trim() || !form.type) {
      toast("type and base_url are required", { type: "error" });
      return;
    }
    const kinds = kindsText
      .split(",")
      .map((k) => k.trim())
      .filter(Boolean);
    mut.mutate({
      ...form,
      base_url: form.base_url.trim(),
      name: form.name?.trim() || undefined,
      country: form.country?.trim().toUpperCase() || undefined,
      listing_path: form.listing_path?.trim() || undefined,
      kinds: kinds.length ? kinds : ["job"],
      recipe: stockRecipe || undefined,
    });
  };

  return (
    <div style={{ maxWidth: 640 }}>
      <div style={{ marginBottom: "1rem" }}>
        <Link to="/sources" style={{ fontSize: "0.85rem" }}>
          ← Sources
        </Link>
        <h1 style={{ margin: "0.35rem 0 0" }}>Add source</h1>
        <p
          style={{
            margin: "0.35rem 0 0",
            color: "var(--c-text-secondary)",
            fontSize: "0.88rem",
          }}
        >
          Creates a source in <code>pending</code>. Next: set listing path,
          verify, approve, install/test a recipe, then crawl.
        </p>
      </div>

      <Card>
        <form onSubmit={onSubmit}>
          <div style={{ marginBottom: "0.85rem" }}>
            <label style={labelStyle} htmlFor="type">
              Connector type *
            </label>
            <select
              id="type"
              value={form.type}
              onChange={(e) => setForm((f) => ({ ...f, type: e.target.value }))}
              style={fieldStyle}
              required
            >
              {SOURCE_TYPES.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </select>
            <p
              style={{
                margin: "0.25rem 0 0",
                fontSize: "0.78rem",
                color: "var(--c-text-secondary)",
              }}
            >
              Engines only — board-specific extract is a recipe, not a type.{" "}
              <code>api</code> / <code>generic_html</code> require a recipe;{" "}
              <code>schema_org</code> / <code>sitemap</code> work without one when
              the site exposes structured data.
            </p>
          </div>

          {form.type === "api" && (
            <div style={{ marginBottom: "0.85rem" }}>
              <label style={labelStyle} htmlFor="stock">
                Stock recipe (optional)
              </label>
              <select
                id="stock"
                value={stockRecipe}
                onChange={(e) => setStockRecipe(e.target.value)}
                style={fieldStyle}
              >
                <option value="">— none (install recipe after create) —</option>
                {STOCK_RECIPES.map((r) => (
                  <option key={r} value={r}>
                    {r}
                  </option>
                ))}
              </select>
              <p
                style={{
                  margin: "0.25rem 0 0",
                  fontSize: "0.78rem",
                  color: "var(--c-text-secondary)",
                }}
              >
                Bundled recipes for well-known public APIs. Host match also
                auto-applies stock recipes on first crawl.
              </p>
            </div>
          )}

          <div style={{ marginBottom: "0.85rem" }}>
            <label style={labelStyle} htmlFor="base_url">
              Base URL *
            </label>
            <input
              id="base_url"
              type="url"
              required
              placeholder="https://careers.example.com/jobs"
              value={form.base_url}
              onChange={(e) =>
                setForm((f) => ({ ...f, base_url: e.target.value }))
              }
              style={fieldStyle}
            />
          </div>

          <div style={{ marginBottom: "0.85rem" }}>
            <label style={labelStyle} htmlFor="listing_path">
              Listing path (relative)
            </label>
            <input
              id="listing_path"
              type="text"
              placeholder="/jobs or empty if base_url is the listing"
              value={form.listing_path}
              onChange={(e) =>
                setForm((f) => ({ ...f, listing_path: e.target.value }))
              }
              style={fieldStyle}
            />
            <p
              style={{
                margin: "0.25rem 0 0",
                fontSize: "0.78rem",
                color: "var(--c-text-secondary)",
              }}
            >
              Definite jobs listing page for recipe generation — never guessed.
            </p>
          </div>

          <div
            style={{
              display: "grid",
              gridTemplateColumns: "1fr 1fr",
              gap: "0.75rem",
              marginBottom: "0.85rem",
            }}
          >
            <div>
              <label style={labelStyle} htmlFor="name">
                Name
              </label>
              <input
                id="name"
                value={form.name}
                onChange={(e) =>
                  setForm((f) => ({ ...f, name: e.target.value }))
                }
                style={fieldStyle}
              />
            </div>
            <div>
              <label style={labelStyle} htmlFor="country">
                Country (ISO)
              </label>
              <input
                id="country"
                placeholder="KE"
                maxLength={3}
                value={form.country}
                onChange={(e) =>
                  setForm((f) => ({ ...f, country: e.target.value }))
                }
                style={fieldStyle}
              />
            </div>
            <div>
              <label style={labelStyle} htmlFor="language">
                Language
              </label>
              <input
                id="language"
                value={form.language}
                onChange={(e) =>
                  setForm((f) => ({ ...f, language: e.target.value }))
                }
                style={fieldStyle}
              />
            </div>
            <div>
              <label style={labelStyle} htmlFor="interval">
                Crawl interval (sec)
              </label>
              <input
                id="interval"
                type="number"
                min={60}
                value={form.crawl_interval_sec}
                onChange={(e) =>
                  setForm((f) => ({
                    ...f,
                    crawl_interval_sec: Number(e.target.value) || 3600,
                  }))
                }
                style={fieldStyle}
              />
            </div>
          </div>

          <div style={{ marginBottom: "0.85rem" }}>
            <label style={labelStyle} htmlFor="kinds">
              Kinds (comma-separated)
            </label>
            <input
              id="kinds"
              value={kindsText}
              onChange={(e) => setKindsText(e.target.value)}
              placeholder="job, scholarship"
              style={fieldStyle}
            />
          </div>

          <label
            style={{
              display: "flex",
              alignItems: "center",
              gap: "0.5rem",
              marginBottom: "1rem",
              fontSize: "0.88rem",
            }}
          >
            <input
              type="checkbox"
              checked={!!form.auto_approve}
              onChange={(e) =>
                setForm((f) => ({ ...f, auto_approve: e.target.checked }))
              }
            />
            Auto-approve after successful verification
          </label>

          {mut.isError && (
            <div style={{ marginBottom: "0.75rem" }}>
              <ErrorBlock
                message="Create failed"
                detail={String(mut.error)}
              />
            </div>
          )}

          <div style={{ display: "flex", gap: "0.5rem" }}>
            <Button type="submit" disabled={mut.isPending}>
              {mut.isPending ? "Creating…" : "Create source"}
            </Button>
            <Link to="/sources">
              <Button type="button" variant="outline">
                Cancel
              </Button>
            </Link>
          </div>
        </form>
      </Card>
    </div>
  );
}
