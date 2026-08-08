package cv

import "testing"

func TestParseContactFromText(t *testing.T) {
	text := `Jane A. Doe
Software Engineer
jane.doe@example.com
+254 712 345 678
Nairobi, Kenya

EXPERIENCE
...
`
	got := ParseContactFromText(text)
	if got.Email != "jane.doe@example.com" {
		t.Fatalf("email=%q", got.Email)
	}
	if got.Phone == "" {
		t.Fatal("expected phone")
	}
	if got.Name != "Jane A. Doe" {
		t.Fatalf("name=%q", got.Name)
	}
}
