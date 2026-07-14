// Package cvstore owns durable CV document metadata and extracted-text
// indexing for matching. Binary content lives in the platform files
// service (or R2 archive fallback); this package stores the local index
// that chat, matching, and re-embed can read without re-fetching the file.
package cvstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// Document is one CV version for a candidate — the local index row.
type Document struct {
	CandidateID string
	Version     int
	// FileID is the platform files-service media id when uploaded there.
	FileID string
	// ContentURI is the durable content URI (files service) or archive key.
	ContentURI string
	// ContentHash is sha256 of the raw bytes (change detection).
	ContentHash string
	Filename    string
	ContentType string
	SizeBytes   int64
	// ExtractedText is plain text used for capabilities, chat, and re-embed.
	// Truncated for storage when very large; TextLength is the original length.
	ExtractedText string
	TextLength    int
	// Storage is "files" | "archive".
	Storage   string
	UpdatedAt time.Time
}

// ContentRef is the result of storing raw CV bytes.
type ContentRef struct {
	FileID      string
	ContentURI  string
	ContentHash string
	SizeBytes   int64
	Storage     string // "files" | "archive"
}

// BlobStore stores raw CV bytes. Production prefers the files service;
// archive.R2 remains the fallback when FileURI is unset.
type BlobStore interface {
	Put(ctx context.Context, in BlobPut) (ContentRef, error)
}

// BlobPut is one raw file upload.
type BlobPut struct {
	CandidateID string
	Filename    string
	ContentType string
	Body        []byte
}

// IndexStore persists the local CV document index (extracted text + pointers).
type IndexStore interface {
	// UpsertDocument writes/replaces the current CV document for the candidate
	// and returns the assigned version number.
	UpsertDocument(ctx context.Context, doc Document) (version int, err error)
	// GetCurrent returns the latest document for a candidate, or nil if none.
	GetCurrent(ctx context.Context, candidateID string) (*Document, error)
}

// ProfilePointers updates candidate_profiles CV columns (best-effort).
type ProfilePointers interface {
	SetCVPointers(ctx context.Context, candidateID, contentURI, contentHash, cvURL string) error
}

// HashBytes returns lowercase hex sha256 of body.
func HashBytes(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// TruncateRunes truncates s to at most max runes.
func TruncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
