package eventsv1

import (
	"fmt"
	"strings"
	"time"
)

// PartKey is the two-dimensional partition key every Parquet file
// inherits: `dt` is the UTC calendar date, `Secondary` is a
// topic-specific axis (source_id for variants, cluster prefix for
// canonicals, lang for translations, cnd prefix for candidates).
type PartKey struct {
	DT        string // YYYY-MM-DD (UTC)
	Secondary string // e.g. "greenhouse" or "en" or "ab" (cluster_id[:2])
}

// PartitionKey chooses the right secondary axis for the given
// event_type. Returning a PartKey keeps the writer's layout one
// central decision.
func PartitionKey(eventType string, occurredAt time.Time, hint string) PartKey {
	dt := occurredAt.UTC().Format("2006-01-02")
	return PartKey{DT: dt, Secondary: partitionSecondary(eventType, hint)}
}

func partitionSecondary(eventType, hint string) string {
	switch eventType {
	case TopicVariantsIngested, TopicCrawlPageCompleted, TopicSourcesDiscovered:
		// per-source files — lots of small sources, keep them grouped
		return strings.ToLower(hint)
	case TopicCanonicalsUpserted, TopicCanonicalsExpired,
		TopicEmbeddings, TopicPublished:
		// cluster_id prefix (2 hex chars = 256 buckets per day)
		return firstN(strings.ToLower(hint), 2)
	case TopicTranslations:
		// hint is the target language code
		return strings.ToLower(hint)
	default:
		// everything else: single bucket per day
		return "_all"
	}
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// ObjectPath returns the R2 object key for a file. `collection` is
// the partition name (variants, canonicals, embeddings, …). `fileID`
// is usually a fresh xid so concurrent writers can never collide.
// Layout examples:
//
//	variants/dt=2026-04-21/src=greenhouse/<xid>.parquet
//	canonicals/dt=2026-04-21/cc=ab/<xid>.parquet
//	translations/dt=2026-04-21/lang=en/<xid>.parquet
func (k PartKey) ObjectPath(collection, fileID string) string {
	label := partitionSecondaryLabel(collection)
	return fmt.Sprintf("%s/dt=%s/%s=%s/%s.parquet",
		collection, k.DT, label, k.Secondary, fileID)
}

// partitionSecondaryLabel maps a collection to its layout-level path
// label. Keeping label mapping separate from the value makes R2
// listings readable — "src=greenhouse" is self-documenting.
func partitionSecondaryLabel(collection string) string {
	switch collection {
	case "variants", "crawl_page_completed", "sources_discovered":
		return "src"
	case "canonicals", "canonicals_expired", "embeddings", "published":
		return "cc"
	case "translations":
		return "lang"
	default:
		return "p"
	}
}
