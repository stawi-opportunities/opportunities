package httpx

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"io"
	"strings"

	"github.com/andybalholm/brotli"
)

// A real browser presents a COHERENT bundle of signals, not just a
// User-Agent: the UA major version must match the Sec-CH-UA brand list,
// it advertises the compressions it can actually decode, and it sends
// the Accept / Accept-Language / Sec-Fetch-* headers a top-level
// navigation carries. Anti-bot gateways (Cloudflare, Akamai,
// DataDome) score on the whole set and on internal consistency, so a
// lone realistic UA with no client hints is often MORE suspicious than
// the default Go UA. We emit one self-consistent Chrome-on-Windows
// profile and, critically, decode whatever it claims to accept.
//
// Limitation: this matches at the HTTP layer only. It does NOT change
// the TLS ClientHello (JA3/JA4) or the HTTP/2 SETTINGS fingerprint,
// which a Go client cannot disguise without uTLS-class machinery — the
// hardest gateways still need the Bright Data unblocker fallback.
const (
	browserUA       = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	browserSecChUA  = `"Google Chrome";v="131", "Chromium";v="131", "Not_A Brand";v="24"`
	browserPlatform = `"Windows"`
	browserAccept   = "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"
	browserLang     = "en-US,en;q=0.9"
	browserAccEnc   = "gzip, deflate, br"
)

// browserHeaders returns the header set a Chrome top-level navigation
// sends. ua overrides the User-Agent ONLY when it already looks like a
// browser (contains "Mozilla/") — a legacy bot UA is replaced by the
// profile UA so it stays coherent with the Sec-CH-UA brand list.
func browserHeaders(ua string) map[string]string {
	if !strings.Contains(ua, "Mozilla/") {
		ua = browserUA
	}
	return map[string]string{
		"User-Agent":                ua,
		"Accept":                    browserAccept,
		"Accept-Language":           browserLang,
		"Accept-Encoding":           browserAccEnc,
		"Sec-Ch-Ua":                 browserSecChUA,
		"Sec-Ch-Ua-Mobile":          "?0",
		"Sec-Ch-Ua-Platform":        browserPlatform,
		"Upgrade-Insecure-Requests": "1",
		"Sec-Fetch-Dest":            "document",
		"Sec-Fetch-Mode":            "navigate",
		"Sec-Fetch-Site":            "none",
		"Sec-Fetch-User":            "?1",
	}
}

// decodeBody reverses Content-Encoding. We must do this ourselves
// because once we set an explicit Accept-Encoding, Go's transport stops
// transparently decompressing (it only auto-handles gzip when the
// caller left Accept-Encoding unset). Output is bounded to maxBodyBytes
// to cap decompression-bomb exposure; raw is already input-bounded by
// the caller. Unknown/empty encodings pass through unchanged — covering
// both identity responses and the transport's own auto-gunzipped bodies
// (which arrive with Content-Encoding stripped).
func decodeBody(encoding string, raw []byte) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "identity":
		return raw, nil
	case "gzip", "x-gzip":
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		defer func() { _ = zr.Close() }()
		return io.ReadAll(io.LimitReader(zr, maxBodyBytes))
	case "br":
		return io.ReadAll(io.LimitReader(brotli.NewReader(bytes.NewReader(raw)), maxBodyBytes))
	case "deflate":
		// "deflate" nominally means zlib-wrapped (RFC 1950), but some
		// servers send raw DEFLATE (RFC 1951); browsers accept both.
		if zr, err := zlib.NewReader(bytes.NewReader(raw)); err == nil {
			defer func() { _ = zr.Close() }()
			return io.ReadAll(io.LimitReader(zr, maxBodyBytes))
		}
		fr := flate.NewReader(bytes.NewReader(raw))
		defer func() { _ = fr.Close() }()
		return io.ReadAll(io.LimitReader(fr, maxBodyBytes))
	default:
		// An encoding we never advertised — don't guess, hand back the
		// raw bytes so a connector can still inspect them.
		return raw, nil
	}
}
