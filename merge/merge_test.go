package merge

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"ontology/phrase"
)

func buildSet(t *testing.T, docsPerSeg int, segs int) *Set {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for g := 0; g < segs; g++ {
		for d := 0; d < docsPerSeg; d++ {
			s.AddDocument([]string{"A", "B", fmt.Sprintf("w%d", d%7)})
		}
		if err := s.Flush(); err != nil {
			t.Fatalf("flush: %v", err)
		}
	}
	return s
}

func hitDocs(hits []phrase.Hit) []uint32 {
	var out []uint32
	for _, h := range hits {
		out = append(out, h.Doc)
	}
	return out
}

func TestMergeCountsAndContent(t *testing.T) {
	s := buildSet(t, 10, 3)
	before := hitDocs(s.Query("A", "B"))
	stats, err := s.MergeSegments()
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	total := 3 * 10 * 3 // 3 terms x 30 docs
	if stats.ItemsRead != total || stats.ItemsWritten != total {
		t.Fatalf("want read==written==%d, got %d/%d", total, stats.ItemsRead, stats.ItemsWritten)
	}
	after := hitDocs(s.Query("A", "B"))
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("query changed across merge: %v vs %v", before, after)
	}
}

func TestDeleteThenMergeEqualsDeleteThenQuery(t *testing.T) {
	cases := []struct {
		name    string
		deleted []uint32
	}{
		{"none", nil},
		{"one", []uint32{3}},
		{"several", []uint32{0, 7, 14, 29}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := buildSet(t, 10, 3)
			for _, d := range tc.deleted {
				s.Delete(d)
			}
			immediate := hitDocs(s.Query("A", "B"))
			for _, d := range tc.deleted {
				for _, got := range immediate {
					if got == d {
						t.Fatalf("deleted doc %d still returned", d)
					}
				}
			}
			if _, err := s.MergeSegments(); err != nil {
				t.Fatalf("merge: %v", err)
			}
			merged := hitDocs(s.Query("A", "B"))
			if !reflect.DeepEqual(immediate, merged) {
				t.Fatalf("immediate %v != merged %v", immediate, merged)
			}
		})
	}
}

func TestMergeCrashRecovery(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for g := 0; g < 2; g++ {
		for d := 0; d < 5; d++ {
			s.AddDocument([]string{"A", "B"})
		}
		if err := s.Flush(); err != nil {
			t.Fatalf("flush: %v", err)
		}
	}
	before := s.Query("A", "B")
	// Simulate a crash mid-merge: a half-written tmp segment appears.
	tmp := filepath.Join(dir, "seg-000002.seg.tmp")
	if err := os.WriteFile(tmp, []byte("ONTSEG01partial"), 0o644); err != nil {
		t.Fatalf("write tmp: %v", err)
	}
	if got := s.Query("A", "B"); !reflect.DeepEqual(before, got) {
		t.Fatalf("query changed after crash: %v vs %v", before, got)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("half-written tmp not cleaned")
	}
	if got := s2.Query("A", "B"); !reflect.DeepEqual(before, got) {
		t.Fatalf("query changed after reopen: %v vs %v", before, got)
	}
}

func TestConcurrentQueryAndMerge(t *testing.T) {
	s := buildSet(t, 20, 4)
	want := s.Query("A", "B")
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				got := s.Query("A", "B")
				if !reflect.DeepEqual(want, got) {
					t.Errorf("inconsistent result: want %d hits got %d", len(want), len(got))
					return
				}
				seen := map[uint32]bool{}
				for _, h := range got {
					if seen[h.Doc] {
						t.Errorf("doc %d duplicated", h.Doc)
						return
					}
					seen[h.Doc] = true
				}
			}
		}()
	}
	for i := 0; i < 5; i++ {
		if _, err := s.MergeSegments(); err != nil {
			t.Fatalf("merge: %v", err)
		}
	}
	close(stop)
	wg.Wait()
}
