package ontology

import (
	"fmt"
	"testing"
)

// seedStore fills the store with keys k00..k(n-1), Value set to i.
func seedStore(n int) *Store {
	s := NewStore()
	for i := 0; i < n; i++ {
		s.Put(Object{Key: fmt.Sprintf("k%02d", i), Value: i})
	}
	return s
}

// drain walks every page using limit and returns all seen keys.
func drain(t *testing.T, s *Store, limit int) []string {
	t.Helper()
	var keys []string
	cursor := ""
	for {
		p, err := s.Scan(cursor, limit)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, o := range p.Items {
			keys = append(keys, o.Key)
		}
		if !p.HasMore {
			// Scanning once more past the end must stay empty, no error.
			again, err := s.Scan(p.Next, limit)
			if err != nil {
				t.Fatalf("terminal rescan error: %v", err)
			}
			if len(again.Items) != 0 || again.HasMore {
				t.Fatalf("terminal rescan not empty: %+v", again)
			}
			return keys
		}
		cursor = p.Next
	}
}

func keysOf(p *Page) []string {
	out := make([]string, len(p.Items))
	for i, o := range p.Items {
		out[i] = o.Key
	}
	return out
}

func assertKeys(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("keys = %v, want %v", got, want)
		}
	}
}
