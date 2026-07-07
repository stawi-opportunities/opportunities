package jobqueue

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/stawi-opportunities/opportunities/pkg/domain"
)

func makeSlug(kind, title, issuer, id string) string {
	digest := sha256.Sum256([]byte(id))
	return domain.BuildSlug(kind, title, issuer, hex.EncodeToString(digest[:4]))
}
