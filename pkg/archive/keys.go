package archive

// RawKey returns the content-addressed key used for candidate documents.
func RawKey(contentHash string) string {
	return "raw/" + contentHash + ".html.gz"
}
