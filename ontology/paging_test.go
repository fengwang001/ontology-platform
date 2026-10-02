package ontology

import "testing"

func TestPagingExactAndOversize(t *testing.T) {
	s, _ := New(exampleSchema(), 3, 1000)
	recs := []map[string]any{
		{"id": int64(1)},
		{"id": int64(2)},
		{"id": int64(3), "author": map[string]any{"ids": []any{int64(5), int64(6)}}},
		{"id": int64(4)},
	}
	for _, r := range recs {
		if err := s.Shred(r); err != nil {
			t.Fatal(err)
		}
	}
	pages, _ := s.Pages("author.ids")
	want := []PageInfo{
		{StartRecord: 0, RecordCount: 3, EntryCount: 4},
		{StartRecord: 3, RecordCount: 1, EntryCount: 1},
	}
	if len(pages) != len(want) {
		t.Fatalf("pages got %v want %v", pages, want)
	}
	for i := range want {
		if pages[i] != want[i] {
			t.Fatalf("page %d got %+v want %+v", i, pages[i], want[i])
		}
	}

	// 单记录条目超过 pageEntries：页不切开记录。
	s2, _ := New([]Field{{Name: "v", Rep: Repeated}}, 3, 1000)
	if err := s2.Shred(map[string]any{"v": []any{int64(1), int64(2), int64(3), int64(4), int64(5)}}); err != nil {
		t.Fatal(err)
	}
	p2, _ := s2.Pages("v")
	if len(p2) != 1 || p2[0].EntryCount != 5 || p2[0].RecordCount != 1 || p2[0].StartRecord != 0 {
		t.Fatalf("oversize record page: %+v", p2)
	}
	if err := s2.Shred(map[string]any{"v": []any{int64(6)}}); err != nil {
		t.Fatal(err)
	}
	p2, _ = s2.Pages("v")
	if len(p2) != 2 {
		t.Fatalf("want 2 pages, got %+v", p2)
	}
	if p2[1] != (PageInfo{StartRecord: 1, RecordCount: 1, EntryCount: 1}) {
		t.Fatalf("second page %+v", p2[1])
	}
}

func TestStats(t *testing.T) {
	s, _ := New(exampleSchema(), 100, 1000)
	if err := s.Shred(map[string]any{"id": int64(10)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Shred(map[string]any{"id": int64(-3)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Shred(map[string]any{"id": int64(42), "author": map[string]any{"name": int64(7)}}); err != nil {
		t.Fatal(err)
	}
	st, _ := s.Stats("id")
	if st.NullCount != 0 || st.PresentCount != 3 || st.Min != -3 || st.Max != 42 {
		t.Fatalf("id stats %+v", st)
	}
	st2, _ := s.Stats("author.name")
	if st2.NullCount != 2 || st2.PresentCount != 1 || st2.Min != 7 || st2.Max != 7 {
		t.Fatalf("name stats %+v", st2)
	}
}

func TestEntriesRead(t *testing.T) {
	s, _ := New(exampleSchema(), 100, 1000)
	for i := int64(0); i < 3; i++ {
		if err := s.Shred(map[string]any{"id": i}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Assemble(); err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, name := range s.LeafNames() {
		es, _ := s.Entries(name)
		total += len(es)
	}
	if got := s.EntriesRead(); got != total || total != 15 {
		t.Fatalf("entriesRead=%d total=%d", got, total)
	}
}
