package applications

import (
	"encoding/base64"
	"encoding/json"
)

func mustEncodeJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic("applications: not JSON-encodable: " + err.Error())
	}
	return b
}

func base64URLEncode(b []byte) string          { return base64.URLEncoding.EncodeToString(b) }
func base64URLDecode(s string) ([]byte, error) { return base64.URLEncoding.DecodeString(s) }
