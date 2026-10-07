package ontology

import "testing"

func mustAddObject(t *testing.T, s *Store, id ObjectID) {
	t.Helper()
	if err := s.AddObject(Object{ID: id, Type: "thing"}); err != nil {
		t.Fatalf("add object %s: %v", id, err)
	}
}

func mustAddLink(t *testing.T, s *Store, id LinkID, from, to ObjectID) {
	t.Helper()
	if err := s.AddLink(Link{ID: id, Type: "rel", From: from, To: to}); err != nil {
		t.Fatalf("add link %s: %v", id, err)
	}
}

func TestSnapshotIsolation(t *testing.T) {
	s := NewStore()
	mustAddObject(t, s, "a")
	mustAddObject(t, s, "b")
	mustAddObject(t, s, "c")
	mustAddLink(t, s, "l1", "a", "b")
	snap := s.Snapshot()
	release := s.Pin(snap)
	defer release()
	// 快照之后的并发修改：删链接、删对象、加新对象与链接。
	if err := s.DeleteLink("l1"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteObject("c"); err != nil {
		t.Fatal(err)
	}
	mustAddObject(t, s, "d")
	mustAddLink(t, s, "l2", "a", "d")
	// 旧快照上：l1 仍可见，c 仍可见，d/l2 不可见。
	cands := s.neighborsAt(snap, "a", "", DirOut)
	if len(cands) != 1 || cands[0].neighbor != "b" {
		t.Fatalf("snapshot view wrong: %v", cands)
	}
	if _, ok := s.objectAt(snap, "c"); !ok {
		t.Fatal("c should be visible on old snapshot")
	}
	if _, ok := s.objectAt(snap, "d"); ok {
		t.Fatal("d should not be visible on old snapshot")
	}
	// 新快照上：l1 与 c 不可见，l2/d 可见。
	now := s.Snapshot()
	cands = s.neighborsAt(now, "a", "", DirOut)
	if len(cands) != 1 || cands[0].neighbor != "d" {
		t.Fatalf("current view wrong: %v", cands)
	}
	if _, ok := s.objectAt(now, "c"); ok {
		t.Fatal("c should be deleted on current snapshot")
	}
}

func TestDeleteObjectCascadesLinks(t *testing.T) {
	s := NewStore()
	mustAddObject(t, s, "a")
	mustAddObject(t, s, "b")
	mustAddLink(t, s, "l1", "a", "b")
	if err := s.DeleteObject("b"); err != nil {
		t.Fatal(err)
	}
	now := s.Snapshot()
	if got := s.neighborsAt(now, "a", "", DirOut); len(got) != 0 {
		t.Fatalf("expected no out links after cascade delete, got %v", got)
	}
	if err := s.DeleteObject("b"); err == nil {
		t.Fatal("double delete should fail")
	}
}

func TestDirectionAndTypeFilter(t *testing.T) {
	s := NewStore()
	mustAddObject(t, s, "a")
	mustAddObject(t, s, "b")
	mustAddObject(t, s, "c")
	mustAddLink(t, s, "l1", "a", "b")
	if err := s.AddLink(Link{ID: "l2", Type: "other", From: "c", To: "a"}); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if got := s.neighborsAt(snap, "a", "", DirOut); len(got) != 1 || got[0].neighbor != "b" {
		t.Fatalf("out: %v", got)
	}
	if got := s.neighborsAt(snap, "a", "", DirIn); len(got) != 1 || got[0].neighbor != "c" {
		t.Fatalf("in: %v", got)
	}
	if got := s.neighborsAt(snap, "a", "", DirBoth); len(got) != 2 {
		t.Fatalf("both: %v", got)
	}
	if got := s.neighborsAt(snap, "a", "other", DirBoth); len(got) != 1 || got[0].neighbor != "c" {
		t.Fatalf("typed: %v", got)
	}
}

func TestGCPreservesPinnedSnapshot(t *testing.T) {
	s := NewStore()
	mustAddObject(t, s, "a")
	mustAddObject(t, s, "b")
	mustAddLink(t, s, "l1", "a", "b")
	snap := s.Snapshot()
	release := s.Pin(snap)
	defer release()
	if err := s.DeleteObject("b"); err != nil {
		t.Fatal(err)
	}
	// 触发一次 GC（通过另一个 pin 的释放），被钉住的快照数据不得被回收。
	r2 := s.Pin(s.Snapshot())
	r2()
	if got := s.neighborsAt(snap, "a", "", DirOut); len(got) != 1 || got[0].neighbor != "b" {
		t.Fatalf("pinned snapshot data must survive GC, got %v", got)
	}
}
