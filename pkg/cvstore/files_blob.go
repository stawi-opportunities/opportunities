package cvstore

import (
	"context"
	"fmt"
	"path"
	"strings"

	"buf.build/gen/go/antinvestor/files/connectrpc/go/files/v1/filesv1connect"
	filesv1 "buf.build/gen/go/antinvestor/files/protocolbuffers/go/files/v1"
)

// FilesBlobStore uploads CV bytes to the antinvestor files service via
// the UploadContent client stream (metadata + binary chunks).
type FilesBlobStore struct {
	Client filesv1connect.FilesServiceClient
	// ChunkSize defaults to 256 KiB when <= 0.
	ChunkSize int
}

// Put streams the CV to the files service and returns the content URI / media id.
func (s *FilesBlobStore) Put(ctx context.Context, in BlobPut) (ContentRef, error) {
	if s == nil || s.Client == nil {
		return ContentRef{}, fmt.Errorf("cvstore: files client is nil")
	}
	if len(in.Body) == 0 {
		return ContentRef{}, fmt.Errorf("cvstore: empty body")
	}
	hash := HashBytes(in.Body)
	ct := in.ContentType
	if ct == "" {
		ct = contentTypeFromName(in.Filename)
	}
	chunk := s.ChunkSize
	if chunk <= 0 {
		chunk = 256 << 10
	}

	stream := s.Client.UploadContent(ctx)
	idem := "cv-" + in.CandidateID + "-" + hash[:16]
	if err := stream.Send(&filesv1.UploadContentRequest{
		Data: &filesv1.UploadContentRequest_Metadata{
			Metadata: &filesv1.UploadMetadata{
				ContentType: ct,
				Filename:    path.Base(in.Filename),
				TotalSize:   int64(len(in.Body)),
				Visibility:  filesv1.MediaMetadata_VISIBILITY_PRIVATE,
			},
		},
		IdempotencyKey: idem,
	}); err != nil {
		return ContentRef{}, fmt.Errorf("cvstore: files upload metadata: %w", err)
	}

	for off := 0; off < len(in.Body); off += chunk {
		end := off + chunk
		if end > len(in.Body) {
			end = len(in.Body)
		}
		if err := stream.Send(&filesv1.UploadContentRequest{
			Data: &filesv1.UploadContentRequest_Chunk{
				Chunk: in.Body[off:end],
			},
			IdempotencyKey: idem,
		}); err != nil {
			return ContentRef{}, fmt.Errorf("cvstore: files upload chunk: %w", err)
		}
	}

	res, err := stream.CloseAndReceive()
	if err != nil {
		return ContentRef{}, fmt.Errorf("cvstore: files upload finish: %w", err)
	}
	msg := res.Msg
	if msg == nil {
		return ContentRef{}, fmt.Errorf("cvstore: files upload empty response")
	}
	uri := msg.GetContentUri()
	fileID := msg.GetMediaId()
	if fileID == "" && msg.GetMetadata() != nil {
		fileID = msg.GetMetadata().GetMediaId()
	}
	if uri == "" && fileID == "" {
		return ContentRef{}, fmt.Errorf("cvstore: files upload missing content_uri/media_id")
	}
	return ContentRef{
		FileID:      fileID,
		ContentURI:  uri,
		ContentHash: hash,
		SizeBytes:   int64(len(in.Body)),
		Storage:     "files",
	}, nil
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
