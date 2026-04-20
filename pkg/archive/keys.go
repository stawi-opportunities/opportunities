// Pure string helpers for the archive bucket's key layout. Kept
// separate from the R2 client so unit tests covering downstream
// callers (crawler, normalize, canonical) can assert exact keys
// without standing up an S3 client.
//
// Layout:
//
//	raw/{sha256}.html.gz                         — dedup'd raw HTTP bodies
//	clusters/{cluster_id}/canonical.json         — current canonical snapshot
//	clusters/{cluster_id}/manifest.json          — mutable index
//	clusters/{cluster_id}/variants/{variant_id}.json
package archive

func RawKey(contentHash string) string {
	return "raw/" + contentHash + ".html.gz"
}

func ClusterDir(clusterID string) string {
	return "clusters/" + clusterID + "/"
}

func CanonicalKey(clusterID string) string {
	return ClusterDir(clusterID) + "canonical.json"
}

func ManifestKey(clusterID string) string {
	return ClusterDir(clusterID) + "manifest.json"
}

func VariantKey(clusterID, variantID string) string {
	return ClusterDir(clusterID) + "variants/" + variantID + ".json"
}
