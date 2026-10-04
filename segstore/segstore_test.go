package segstore

import (
	"errors"
	"testing"
)

func d(id string, v int64) Doc { return Doc{ID: id, SortVal: v} }

func TestAddSegmentValidation(t *testing.T) {
	s := New()
	cases := []struct {
		name string
		docs []Doc
		want error
	}{
		{"empty", nil, ErrInvalid},
		{"too many", func() []Doc {
			ds := make([]Doc, 10001)
			for i := range ds {
				ds[i] = d(string(rune('a'+i%26))+itoa(i), 1)
			}
			return ds
		}(), nil}, // only the count is checked below separately
		{"empty id", []Doc{d("", 1)}, ErrInvalid},
		{"dup in batch", []Doc{d("a", 1), d("a", 2)}, ErrInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "too many" {
				if _, err := s.AddSegment(tc.docs); !errors.Is(err, ErrInvalid) {
					t.Fatalf("got %v want ErrInvalid", err)
				}
				return
			}
			if tc.want == nil {
				return
			}
			if _, err := s.AddSegment(tc.docs); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}

	s2 := New()
	if id, err := s2.AddSegment([]Doc{d("a", 5)}); err != nil || id != 1 {
		t.Fatalf("first seg: id=%d err=%v", id, err)
	}
	if _, err := s2.AddSegment([]Doc{d("a", 9)}); !errors.Is(err, ErrIDConflict) {
		t.Fatalf("conflict: got %v", err)
	}
	if id, err := s2.AddSegment([]Doc{d("b", 1), d("c", 1)}); err != nil || id != 2 {
		t.Fatalf("second seg: id=%d err=%v", id, err)
	}
}

func TestOrderingAndKeys(t *testing.T) {
	s := New()
	id, err := s.AddSegment([]Doc{d("b", 7), d("a", 5), d("c", 5)})
	if err != nil || id != 1 {
		t.Fatalf("add: id=%d err=%v", id, err)
	}
	got := s.ScanLocked([]int{1}, 100, nil, 10)
	want := []Entry{
		{Doc: d("a", 5), Key: Key{5, 1, 0}},
		{Doc: d("c", 5), Key: Key{5, 1, 1}},
		{Doc: d("b", 7), Key: Key{7, 1, 2}},
	}
	assertEntries(t, got, want)
}

func TestDeleteVisibility(t *testing.T) {
	s := New()
	_, _ = s.AddSegment([]Doc{d("a", 5), d("b", 7)})
	if err := s.Delete("b", 5); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("b", 6); !errors.Is(err, ErrDocNotFound) {
		t.Fatalf("double delete got %v", err)
	}
	if err := s.Delete("zzz", 7); !errors.Is(err, ErrDocNotFound) {
		t.Fatalf("missing delete got %v", err)
	}
	if es := s.ScanLocked([]int{1}, 4, nil, 10); len(es) != 2 {
		t.Fatalf("op=4 should still see b, got %d", len(es))
	}
	if es := s.ScanLocked([]int{1}, 5, nil, 10); len(es) != 1 || es[0].ID != "a" {
		t.Fatalf("op=5 should hide b, got %v", es)
	}
}

func TestMerge(t *testing.T) {
	s := New()
	if _, err := s.MergeLocked([]int{1, 2}); !errors.Is(err, ErrSegNotFound) {
		t.Fatalf("merge empty got %v", err)
	}
	_, _ = s.AddSegment([]Doc{d("a", 5), d("b", 7)})
	_, _ = s.AddSegment([]Doc{d("c", 5), d("d", 9)})
	if _, err := s.MergeLocked([]int{1, 1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("dup seg got %v", err)
	}
	if _, err := s.MergeLocked([]int{1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("one seg got %v", err)
	}

	if err := s.Delete("b", 1); err != nil {
		t.Fatal(err)
	}
	id, err := s.MergeLocked([]int{1, 2})
	if err != nil || id != 3 {
		t.Fatalf("merge id=%d err=%v", id, err)
	}
	got := s.ScanLocked([]int{3}, 2, nil, 10)
	want := []Entry{
		{Doc: d("a", 5), Key: Key{5, 3, 0}},
		{Doc: d("c", 5), Key: Key{5, 3, 1}},
		{Doc: d("d", 9), Key: Key{9, 3, 2}},
	}
	assertEntries(t, got, want)

	// Old segments leave the view and are physically released.
	if rel := s.Released(); len(rel) != 2 || rel[0] != 1 || rel[1] != 2 {
		t.Fatalf("released=%v", rel)
	}
	if _, err := s.MergeLocked([]int{1, 3}); !errors.Is(err, ErrSegNotFound) {
		t.Fatalf("stale seg got %v", err)
	}
}

func TestMergeNoSurvivors(t *testing.T) {
	s := New()
	a, _ := s.AddSegment([]Doc{d("a", 1)})
	b, _ := s.AddSegment([]Doc{d("b", 2)})
	_ = s.Delete("a", 1)
	_ = s.Delete("b", 2)
	id, err := s.MergeLocked([]int{a, b})
	if err != nil || id != 0 {
		t.Fatalf("empty merge id=%d err=%v", id, err)
	}
	if len(s.ViewLocked()) != 0 {
		t.Fatalf("view should be empty, got %v", s.ViewLocked())
	}
	if next := s.nextSeg; next != 3 {
		t.Fatalf("no segment number should be consumed, next=%d", next)
	}
	// Adding after an empty merge still yields the next sequential id.
	id, _ = s.AddSegment([]Doc{d("c", 3)})
	if id != 3 {
		t.Fatalf("new seg id=%d want 3", id)
	}
}

func TestReferenceCounting(t *testing.T) {
	s := New()
	_, _ = s.AddSegment([]Doc{d("a", 1)})
	_, _ = s.AddSegment([]Doc{d("b", 2)})
	s.AcquireLocked(1) // simulate a PIT holding seg 1
	_, err := s.MergeLocked([]int{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if rel := s.Released(); len(rel) != 1 || rel[0] != 2 {
		t.Fatalf("only seg 2 frees, got %v", rel)
	}
	s.ReleaseLocked(1)
	s.FlushReleasedLocked([]int{1})
	if rel := s.Released(); len(rel) != 2 || rel[0] != 2 || rel[1] != 1 {
		t.Fatalf("seg 1 frees last, cross-op append order [2,1], got %v", rel)
	}
}

func TestAfterKeyPaging(t *testing.T) {
	s := New()
	_, _ = s.AddSegment([]Doc{d("b", 5), d("a", 5), d("c", 6)})
	page1 := s.ScanLocked([]int{1}, 0, nil, 2)
	if len(page1) != 2 {
		t.Fatalf("page1 len=%d", len(page1))
	}
	last := page1[1].Key
	page2 := s.ScanLocked([]int{1}, 0, &last, 2)
	if len(page2) != 1 || page2[0].ID != "c" {
		t.Fatalf("page2=%v", page2)
	}
	// A non-existent after key still pages correctly.
	es := s.ScanLocked([]int{1}, 0, &Key{SortVal: 5, Seg: 1, Idx: 5}, 10)
	if len(es) != 1 || es[0].ID != "c" {
		t.Fatalf("synthetic after=%v", es)
	}
}

func assertEntries(t *testing.T, got, want []Entry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len got=%d want=%d (%v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("[%d] got=%+v want=%+v", i, got[i], want[i])
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
