// Package authz centralizes auth helpers shared by the API surfaces.
//
// The platform's downstream Go services do not verify JWT signatures
// themselves — an upstream gateway (Frame's SecurityManager) does
// that. The services consume the already-verified Bearer token to
// extract the candidate identity. Helpers here keep the extraction
// logic in one place so promoting from "trust gateway" to "verify
// locally" later only touches this package.
package authz

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
)

// ProfileIDFromJWT extracts the sub claim from the Authorization Bearer
// token without verifying the signature. Verification is the gateway's
// responsibility (see apps/crawler/cmd/main.go for the SecurityManager
// pattern); downstream Go services trust the token.
//
// Returns "" when no Bearer token is present or the payload cannot be
// decoded. The caller MUST treat an empty string as "unauthenticated"
// and refuse to act on candidate-scoped resources.
func ProfileIDFromJWT(req *http.Request) string {
	if req == nil {
		return ""
	}
	auth := req.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	token := strings.TrimSpace(auth[len("Bearer "):])
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return claims.Sub
}
