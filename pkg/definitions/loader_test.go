package definitions_test

import (
	"context"
	"testing"

	"github.com/stawi-opportunities/opportunities/pkg/definitions"
)

func TestMemoryLoader_PutGet(t *testing.T) {
	l := definitions.NewMemoryLoader()
	body := []byte("kind: job\nuniversal_required: [title]")
	ver := l.Put(definitions.TypeKind, "job", body)
	if ver == "" {
		t.Fatal("Put returned empty version")
	}
	got, v, err := l.Get(context.Background(), definitions.TypeKind, "job")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != string(body) || v != ver {
		t.Fatalf("got = (%q, %q); want (%q, %q)", got, v, body, ver)
	}
}

func TestMemoryLoader_Get_NotFound(t *testing.T) {
	l := definitions.NewMemoryLoader()
	_, _, err := l.Get(context.Background(), definitions.TypeKind, "nope")
	if err != definitions.ErrNotFound {
		t.Fatalf("err = %v; want ErrNotFound", err)
	}
}

func TestMemoryLoader_Subscribe_FiresOnPut(t *testing.T) {
	l := definitions.NewMemoryLoader()
	fired := 0
	l.Subscribe(definitions.TypeKind, func(name, version string) {
		if name == "job" {
			fired++
		}
	})
	l.Put(definitions.TypeKind, "job", []byte("x"))
	l.Put(definitions.TypeKind, "scholarship", []byte("y")) // shouldn't count
	if fired != 1 {
		t.Fatalf("fired = %d; want 1", fired)
	}
}

func TestMemoryLoader_List_SortedByName(t *testing.T) {
	l := definitions.NewMemoryLoader()
	l.Put(definitions.TypeKind, "tender", []byte("x"))
	l.Put(definitions.TypeKind, "job", []byte("y"))
	l.Put(definitions.TypeKind, "scholarship", []byte("z"))
	entries, _ := l.List(context.Background(), definitions.TypeKind)
	if len(entries) != 3 {
		t.Fatalf("len = %d; want 3", len(entries))
	}
	if entries[0].Name != "job" || entries[1].Name != "scholarship" || entries[2].Name != "tender" {
		t.Fatalf("order wrong: %v", entries)
	}
}
