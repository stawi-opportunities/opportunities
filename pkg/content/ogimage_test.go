package content

import "testing"

func TestOGImage(t *testing.T) {
	cases := []struct{ name, html, want string }{
		{"og:image", `<head><meta property="og:image" content="https://x.com/logo.png"></head>`, "https://x.com/logo.png"},
		{"reversed attr order", `<meta content="https://x.com/a.png" property="og:image"/>`, "https://x.com/a.png"},
		{"secure_url variant", `<meta property="og:image:secure_url" content="https://x.com/s.png">`, "https://x.com/s.png"},
		{"single quotes", `<meta property='og:image' content='https://x.com/q.png'>`, "https://x.com/q.png"},
		{"twitter fallback", `<meta name="twitter:image" content="https://x.com/t.png">`, "https://x.com/t.png"},
		{"prefer og over twitter", `<meta name="twitter:image" content="https://t.png"><meta property="og:image" content="https://og.png">`, "https://og.png"},
		{"none", `<head><title>x</title></head>`, ""},
		{"relative skipped", `<meta property="og:image" content="/rel.png">`, ""},
		{"unescapes entities", `<meta property="og:image" content="https://x.com/a.png?a=1&amp;b=2">`, "https://x.com/a.png?a=1&b=2"},
		{"empty", ``, ""},
	}
	for _, c := range cases {
		if got := OGImage(c.html); got != c.want {
			t.Errorf("%s: OGImage = %q, want %q", c.name, got, c.want)
		}
	}
}
