package ontology

import (
	"reflect"
	"sync"
	"testing"
)

func TestConcurrentShred(t *testing.T) {
	s, _ := New(exampleSchema(), 7, 10000)
	const writers = 8
	const per = 60
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				rec := map[string]any{"id": int64(base*per + i)}
				if i%3 == 0 {
					rec["author"] = map[string]any{"ids": []any{int64(i), int64(i + 1)}}
				}
				if err := s.Shred(rec); err != nil {
					t.Error(err)
					return
				}
			}
		}(w)
	}
	// 并发读取者
	stop := make(chan struct{})
	var rwg sync.WaitGroup
	rwg.Add(2)
	for r := 0; r < 2; r++ {
		go func() {
			defer rwg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_, _ = s.Entries("id")
					_, _ = s.Pages("author.ids")
					_, _ = s.Stats("id")
					_ = s.Records()
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	rwg.Wait()

	if got := s.Records(); got != writers*per {
		t.Fatalf("records=%d want %d", got, writers*per)
	}
	out, err := s.Assemble()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != writers*per {
		t.Fatalf("assembled %d", len(out))
	}
	// 全部 id 无重复、无丢失。
	seen := map[int64]bool{}
	for _, rec := range out {
		id := rec["id"].(int64)
		if seen[id] {
			t.Fatalf("duplicate id %d", id)
		}
		seen[id] = true
	}
	if len(seen) != writers*per {
		t.Fatalf("unique ids %d", len(seen))
	}

	// 重放确定性：另建一个 Shredder，按 Assemble 结果重新 Shred，条目必须一致。
	s2, _ := New(exampleSchema(), 7, 10000)
	for _, rec := range out {
		if err := s2.Shred(rec); err != nil {
			t.Fatal(err)
		}
	}
	out2, err := s2.Assemble()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, out2) {
		t.Fatal("replay not deterministic")
	}
}

func TestUnknownColumn(t *testing.T) {
	s, _ := New(exampleSchema(), 4, 100)
	if _, err := s.Entries("nope"); !isSchemaErr(err) {
		t.Fatalf("Entries unknown: %v", err)
	}
	if _, err := s.Pages("nope"); !isSchemaErr(err) {
		t.Fatalf("Pages unknown: %v", err)
	}
	if _, err := s.Stats("nope"); !isSchemaErr(err) {
		t.Fatalf("Stats unknown: %v", err)
	}
}

func isSchemaErr(err error) bool {
	return err != nil && ErrorPath(err) == "nope"
}
