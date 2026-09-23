package compact

import (
	"ontology/level"
	"ontology/segment"
	"os"
	"path/filepath"
	"testing"
)

func TestTombstoneLifecycle(t *testing.T) {
	cases := []struct {
		name      string
		from      []int
		to        int
		wantState segment.State
		wantCount int // 新段条目数
	}{
		{"partial compact keeps tombstone", []int{0, 1}, 1, segment.StateDeleted, 3},
		{"full compact drops tombstone", []int{0, 1, 2}, 2, segment.StateAbsent, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := level.OpenStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			ingest(t, s, 2, [3]string{"k", "L2old", ""})
			ingest(t, s, 1, [3]string{"a", "1", ""})
			ingest(t, s, 0, [3]string{"k", "", "del"}, [3]string{"z", "26", ""})
			before := totalSize(t, s)
			meta, err := Compact(s, c.from, c.to)
			if err != nil {
				t.Fatal(err)
			}
			_, st, err := s.Get([]byte("k"))
			if err != nil || st != c.wantState {
				t.Fatalf("Get(k) state=%v err=%v, want %v", st, err, c.wantState)
			}
			if meta.Count != c.wantCount {
				t.Fatalf("merged count=%d, want %d", meta.Count, c.wantCount)
			}
			if c.wantState == segment.StateAbsent {
				if after := totalSize(t, s); after >= before {
					t.Fatalf("size not shrunk: before=%d after=%d", before, after)
				}
			}
		})
	}
}

func ingest(t *testing.T, s *level.Store, lvl int, kvs ...[3]string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "w.seg")
	w, _ := segment.NewWriter(p)
	for _, e := range kvs {
		if e[2] == "del" {
			w.Delete([]byte(e[0]))
		} else {
			w.Add([]byte(e[0]), []byte(e[1]))
		}
	}
	w.Close()
	if _, err := s.Ingest(p, lvl); err != nil {
		t.Fatal(err)
	}
}

func totalSize(t *testing.T, s *level.Store) int64 {
	t.Helper()
	var sum int64
	for _, ms := range s.Snapshot() {
		for _, m := range ms {
			fi, err := os.Stat(m.Path)
			if err != nil {
				t.Fatal(err)
			}
			sum += fi.Size()
		}
	}
	return sum
}
