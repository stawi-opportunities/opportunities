package placement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"strings"

	"buf.build/gen/go/antinvestor/files/connectrpc/go/files/v1/filesv1connect"
	filesv1 "buf.build/gen/go/antinvestor/files/protocolbuffers/go/files/v1"

	"github.com/stawi-opportunities/opportunities/pkg/archive"
)

// FileRef is the durable reference returned after storing a CV binary.
type FileRef struct {
	FileID      string // platform files-service media id (preferred)
	ContentURI  string // download / content URI
	ContentHash string // sha256 of raw bytes
	SizeBytes   int64
	Storage     string // "files" | "archive"
}

// FileStore stores CV bytes. Production uses the platform files service.
type FileStore interface {
	Put(ctx context.Context, candidateID, filename, contentType string, body []byte) (FileRef, error)
}

// FilesServiceStore uploads via antinvestor FilesService.UploadContent.
type FilesServiceStore struct {
	Client    filesv1connect.FilesServiceClient
	ChunkSize int
}

// Put streams the CV to the files service and returns media id + URI.
func (s *FilesServiceStore) Put(ctx context.Context, candidateID, filename, contentType string, body []byte) (FileRef, error) {
	if s == nil || s.Client == nil {
		return FileRef{}, fmt.Errorf("placement: files client is nil")
	}
	if len(body) == 0 {
		return FileRef{}, fmt.Errorf("placement: empty body")
	}
	hash := hashBytes(body)
	ct := contentType
	if ct == "" {
		ct = contentTypeFromName(filename)
	}
	chunk := s.ChunkSize
	if chunk <= 0 {
		chunk = 256 << 10
	}

	stream := s.Client.UploadContent(ctx)
	idem := "cv-" + candidateID + "-" + hash[:16]
	if err := stream.Send(&filesv1.UploadContentRequest{
		Data: &filesv1.UploadContentRequest_Metadata{
			Metadata: &filesv1.UploadMetadata{
				ContentType: ct,
				Filename:    path.Base(filename),
				TotalSize:   int64(len(body)),
				Visibility:  filesv1.MediaMetadata_VISIBILITY_PRIVATE,
			},
		},
		IdempotencyKey: idem,
	}); err != nil {
		return FileRef{}, fmt.Errorf("placement: files upload metadata: %w", err)
	}

	for off := 0; off < len(body); off += chunk {
		end := off + chunk
		if end > len(body) {
			end = len(body)
		}
		if err := stream.Send(&filesv1.UploadContentRequest{
			Data: &filesv1.UploadContentRequest_Chunk{
				Chunk: body[off:end],
			},
			IdempotencyKey: idem,
		}); err != nil {
			return FileRef{}, fmt.Errorf("placement: files upload chunk: %w", err)
		}
	}

	res, err := stream.CloseAndReceive()
	if err != nil {
		return FileRef{}, fmt.Errorf("placement: files upload finish: %w", err)
	}
	msg := res.Msg
	if msg == nil {
		return FileRef{}, fmt.Errorf("placement: files upload empty response")
	}
	uri := msg.GetContentUri()
	fileID := msg.GetMediaId()
	if fileID == "" && msg.GetMetadata() != nil {
		fileID = msg.GetMetadata().GetMediaId()
	}
	if uri == "" && fileID == "" {
		return FileRef{}, fmt.Errorf("placement: files upload missing content_uri/media_id")
	}
	return FileRef{
		FileID:      fileID,
		ContentURI:  uri,
		ContentHash: hash,
		SizeBytes:   int64(len(body)),
		Storage:     "files",
	}, nil
}

// ArchiveFileStore is a dev/fallback when FILE_SERVICE_URI is unset.
// Prefer FilesServiceStore in production.
type ArchiveFileStore struct {
	Archive archive.Archive
}

// Put archives raw bytes and returns a content-addressed archive key.
func (s *ArchiveFileStore) Put(ctx context.Context, candidateID, filename, contentType string, body []byte) (FileRef, error) {
	_ = candidateID
	_ = filename
	_ = contentType
	if s == nil || s.Archive == nil {
		return FileRef{}, fmt.Errorf("placement: archive is nil")
	}
	hash, size, err := s.Archive.PutRaw(ctx, body)
	if err != nil {
		return FileRef{}, fmt.Errorf("placement: archive PutRaw: %w", err)
	}
	return FileRef{
		FileID:      "",
		ContentURI:  archive.RawKey(hash),
		ContentHash: hashBytes(body),
		SizeBytes:   size,
		Storage:     "archive",
	}, nil
}

func hashBytes(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func contentTypeFromName(name string) string {
	ext := strings.ToLower(path.Ext(name))
	switch ext {
	case ".pdf":
		return "application/pdf"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".txt", ".text", ".md":
		return "text/plain"
	case ".rtf":
		return "application/rtf"
	default:
		return "application/octet-stream"
	}
}
