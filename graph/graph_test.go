package graph

import "testing"

func TestStoreCRUD(t *testing.T) {
	s := NewStore()
	if err := s.AddObject(Object{ID: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddObject(Object{ID: "a"}); err == nil {
		t.Fatal("duplicate object should fail")
	}
	if err := s.AddLink(Link{ID: "l1", SourceID: "a", TargetID: "b"}); err == nil {
		t.Fatal("link with missing endpoint should fail")
	}
	if err := s.AddObject(Object{ID: "b"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddLink(Link{ID: "l1", SourceID: "a", TargetID: "b"}); err != nil {
		t.Fatal(err)
	}
	if got := len(s.Snapshot().Links("a", Outgoing)); got != 1 {
		t.Fatalf("outgoing links = %d, want 1", got)
	}
	if got := len(s.Snapshot().Links("b", Incoming)); got != 1 {
		t.Fatalf("incoming links = %d, want 1", got)
	}
	if err := s.RemoveObject("a"); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if snap.HasObject("a") {
		t.Fatal("object a should be removed")
	}
	if got := len(snap.Links("b", Incoming)); got != 0 {
		t.Fatalf("cascaded link should be removed, got %d", got)
	}
}

// 存储层快照：快照之后的修改对快照不可见。
func TestSnapshotIsImmutableView(t *testing.T) {
	s := NewStore()
	_ = s.AddObject(Object{ID: "a"})
	_ = s.AddObject(Object{ID: "b"})
	_ = s.AddLink(Link{ID: "l1", SourceID: "a", TargetID: "b"})

	snap := s.Snapshot()
	v := snap.Version()

	_ = s.RemoveLink("l1")
	_ = s.AddLink(Link{ID: "l2", SourceID: "b", TargetID: "a"})

	if snap.Version() != v {
		t.Fatal("snapshot version changed")
	}
	if got := len(snap.Links("a", Outgoing)); got != 1 {
		t.Fatalf("snapshot should still see l1, got %d links", got)
	}
	if got := len(snap.Links("b", Incoming)); got != 1 {
		t.Fatalf("snapshot should still see l1 incoming, got %d links", got)
	}
	if got := len(s.Snapshot().Links("a", Outgoing)); got != 0 {
		t.Fatalf("live store should not see l1, got %d links", got)
	}
}
