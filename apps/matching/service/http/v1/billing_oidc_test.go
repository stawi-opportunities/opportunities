package v1

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/pitabwire/frame/v2/security"
	"github.com/stretchr/testify/require"
)

func TestOidcEmailFromContext_FromJWTPayload(t *testing.T) {
	t.Parallel()
	payload, err := json.Marshal(map[string]any{
		"sub":   "profile-1",
		"email": "peter@example.com",
	})
	require.NoError(t, err)
	// header.payload.sig — only payload is read; signature ignored.
	tok := "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(payload) + ".x"
	ctx := security.JwtToContext(context.Background(), tok)
	require.Equal(t, "peter@example.com", oidcEmailFromContext(ctx))
}

func TestOidcEmailFromContext_Empty(t *testing.T) {
	t.Parallel()
	require.Equal(t, "", oidcEmailFromContext(context.Background()))
}
