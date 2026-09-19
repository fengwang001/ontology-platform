package ontology

import "testing"

func TestPutGetUpdateDelete(t *testing.T) {
	s := NewStore()
	s.Put("b", 2)
	s.Put("a", 1)
	s.Put("c", 3)
	if v, ok := s.Get("a"); !ok || v != 1 {
		t.Fatalf("Get(a) = %d,%v", v, ok)
	}
	s.Put("a", 10) // update, not a new insert
	if v, _ := s.Get("a"); v != 10 {
		t.Fatalf("after update Get(a) = %d", v)
	}
	s.Delete("b")
	if _, ok := s.Get("b"); ok {
		t.Fatalf("b should be deleted")
	}
	s.Delete("missing") // no-op, must not panic
	p, err := s.Scan("", 10)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if got := pageKeys(p); !equalKeys(got, []string{"a", "c"}) {
		t.Fatalf("keys = %v, want [a c]", got)
	}
	if p.Items[0].Value != 10 || p.Items[1].Value != 3 {
		t.Fatalf("values = %v", p.Items)
	}
}

func TestScanEmptyStore(t *testing.T) {
	s := NewStore()
	p, err := s.Scan("", 5)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(p.Items) != 0 || p.HasMore {
		t.Fatalf("empty store: %d items, hasMore=%v", len(p.Items), p.HasMore)
	}
	tail, err := s.Scan(p.Next, 5)
	if err != nil || len(tail.Items) != 0 || tail.HasMore {
		t.Fatalf("tail on empty store: %d items, hasMore=%v, err=%v",
			len(tail.Items), tail.HasMore, err)
	}
}

func TestInsertAfterSnapshotInvisible(t *testing.T) {
	s, _ := seed(t, 3)
	p1, _ := s.Scan("", 1) // snapshot: k000..k002
	s.Put("k001a", 99)
	rest, _, _ := drain(t, s, p1.Next, 1)
	all := append(pageKeys(p1), rest...)
	if contains(all, "k001a") {
		t.Fatalf("post-snapshot insert visible: %v", all)
	}
	if len(all) != 3 {
		t.Fatalf("got %v, want 3 snapshot elements", all)
	}
}
