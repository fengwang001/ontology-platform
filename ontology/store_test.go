package ontology

import "testing"

func TestStoreBasics(t *testing.T) {
	s := NewStore()
	s.EnsureType("A")
	s.EnsureType("A")
	if !s.HasType("A") || s.HasType("B") {
		t.Fatal("EnsureType/HasType")
	}
	if !s.CreateObject("a1", "A", map[string]float64{"score": 3}) {
		t.Fatal("create should succeed")
	}
	if s.CreateObject("a1", "A", nil) {
		t.Fatal("duplicate create should fail")
	}
	if ty, ok := s.ObjectTypeOf("a1"); !ok || ty != "A" {
		t.Fatalf("ObjectTypeOf = %q,%v", ty, ok)
	}
	if v, ok := s.Attr("a1", "score"); !ok || v != 3 {
		t.Fatalf("Attr = %v,%v", v, ok)
	}
	if _, ok := s.Attr("a1", "missing"); ok {
		t.Fatal("missing attr should be absent")
	}
	if !s.HasObject("a1") || s.HasObject("ghost") {
		t.Fatal("HasObject")
	}

	s.CreateObject("a2", "A", nil)
	l := &Link{ID: "l1", From: "a1", Rel: "r", To: "a2"}
	s.Lock()
	if !s.AddLinkLocked(l) {
		t.Fatal("add link")
	}
	if s.AddLinkLocked(l) {
		t.Fatal("duplicate link id must fail")
	}
	if got := s.OutgoingLocked("a1"); len(got) != 1 || got[0].ID != "l1" {
		t.Fatalf("OutgoingLocked = %+v", got)
	}
	if got := s.IncomingLocked("a2"); len(got) != 1 {
		t.Fatalf("IncomingLocked = %+v", got)
	}
	old, had := s.WriteAttrLocked("a1", "score", 9)
	if !had || old != 3 {
		t.Fatalf("WriteAttrLocked = %v,%v", old, had)
	}
	if v, _ := s.AttrLocked("a1", "score"); v != 9 {
		t.Fatalf("AttrLocked = %v", v)
	}
	if r := s.RemoveLinkLocked("l1"); r == nil || r.ID != "l1" {
		t.Fatal("remove link")
	}
	if got := s.OutgoingLocked("a1"); len(got) != 0 {
		t.Fatalf("outgoing after remove = %+v", got)
	}
	if s.RemoveLinkLocked("l1") != nil {
		t.Fatal("removing again returns nil")
	}
	s.Unlock()

	if objs := s.Objects(); len(objs) != 2 {
		t.Fatalf("Objects = %d", len(objs))
	}
	if links := s.Links(); len(links) != 0 {
		t.Fatalf("Links = %d", len(links))
	}
}

func TestErrorPriorities(t *testing.T) {
	if got := HighestPriority(nil); got != nil {
		t.Fatal("nil candidates -> nil")
	}
	got := HighestPriority(
		NewMaintenanceFailedError("late"),
		NewTypeMismatchError("first"),
		NewInstanceNotFoundError("middle"),
	)
	if got == nil || got.Kind != KindTypeMismatch {
		t.Fatalf("highest priority = %+v", got)
	}
}
