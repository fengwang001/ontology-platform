package ontology

import (
	"sync"
	"testing"
)

func TestConcurrentShredAndRead(t *testing.T) {
	s, _ := New(exampleSchema(), 7, 1000)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				rec := map[string]any{
					"id": int64(base*100 + i),
					"author": map[string]any{
						"ids": []any{int64(i), int64(i + 1)},
					},
				}
				if err := s.Shred(rec); err != nil {
					t.Error(err)
					return
				}
				_ = s.Entries("author.ids")
				_ = s.Pages("id")
				_ = s.Stats("author.ids")
			}
		}(g)
	}
	wg.Wait()

	if n := s.RecordCount(); n != 800 {
		t.Fatalf("record count = %d want 800", n)
	}
	if es := s.Entries("id"); len(es) != 800 {
		t.Fatalf("id entries = %d", len(es))
	}
	if es := s.Entries("author.ids"); len(es) != 1600 {
		t.Fatalf("author.ids entries = %d", len(es))
	}
	recs := s.Assemble()
	if len(recs) != 800 {
		t.Fatalf("assembled %d", len(recs))
	}
	seen := map[int64]bool{}
	for _, r := range recs {
		id := r["id"].(int64)
		if seen[id] {
			t.Fatalf("duplicate id %d", id)
		}
		seen[id] = true
		ids := r["author"].(map[string]any)["ids"].([]any)
		if ids[0] != id%100 || ids[1] != id%100+1 {
			t.Fatalf("bad ids for %d: %#v", id, ids)
		}
	}
}
