package v1_test

import (
	"net/http"
	"testing"

	v1 "github.com/stawi-opportunities/opportunities/apps/applications/service/http/v1"
	"github.com/stawi-opportunities/opportunities/pkg/applications"
)

func TestMount_CompilesWithFullDepsSet(t *testing.T) {
	mux := http.NewServeMux()
	v1.Mount(mux, &v1.Deps{
		Store:            applications.NewStore(nil),
		EventLog:         applications.NewEventLog(nil),
		NotesStore:       applications.NewNotesStore(nil),
		RemindersStore:   applications.NewRemindersStore(nil),
		AttachmentsStore: applications.NewAttachmentsStore(nil),
		BlobStore:        applications.NewMemoryBlobStore(),
		Idempotency:      applications.NewIdempotencyStore(nil, 0),
	})
	_ = mux
}
