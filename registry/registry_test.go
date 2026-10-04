package registry

import "testing"

func stagesForTest() []Stage {
	return []Stage{
		{Name: "dev", S: 0, Trusted: map[string]bool{}},
		{Name: "rel", Immutable: true, S: 10, Trusted: map[string]bool{}},
	}
}

func TestPlaceImmutableTombAndAliasClash(t *testing.T) {
	s, err := NewStore(stagesForTest())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Place(1, "a", "t", "d1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Place(1, "a", "t", "d1"); err != nil {
		t.Fatalf("same digest idempotent: %v", err)
	}
	if err := s.Place(1, "a", "t", "d2"); err != ErrImmutable {
		t.Fatalf("immutable: %v", err)
	}
	if err := s.SetAlias(1, "a", "st", "t"); err != nil {
		t.Fatal(err)
	}
	if err := s.Place(1, "a", "st", "d9"); err != ErrAliasClash {
		t.Fatalf("tag/alias clash: %v", err)
	}
	// 被别名指向的标签不可 Yank。
	if err := s.Yank(1, "a", "t"); err != ErrReferenced {
		t.Fatalf("referenced yank: %v", err)
	}
	// 墓碑拒绝同摘要复用：在独立 store 上验证（上一 store 的 t 仍被引用）。
	s2, _ := NewStore(stagesForTest())
	if err := s2.Place(1, "b", "t", "d1"); err != nil {
		t.Fatal(err)
	}
	if err := s2.Yank(1, "b", "t"); err != nil {
		t.Fatal(err)
	}
	if err := s2.Place(1, "b", "t", "d1"); err != ErrTagYanked {
		t.Fatalf("tombstone same digest: %v", err)
	}
	// 可变级 Yank 物理删除，可重用。
	if err := s.Place(0, "a", "t", "d1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Yank(0, "a", "t"); err != nil {
		t.Fatal(err)
	}
	if err := s.Place(0, "a", "t", "d2"); err != nil {
		t.Fatalf("mutable reuse: %v", err)
	}
}

func TestResolveTouchedAtMostTwo(t *testing.T) {
	s, _ := NewStore(stagesForTest())
	_ = s.Place(0, "a", "v1", "d1")
	_ = s.SetAlias(0, "a", "stable", "v1")
	if d, err := s.Resolve("dev", "a", "stable"); err != nil || d != "d1" {
		t.Fatalf("resolve alias: %q %v", d, err)
	}
	if s.Touched() > 2 {
		t.Fatalf("touched=%d", s.Touched())
	}
	if d, err := s.Resolve("dev", "a", "v1"); err != nil || d != "d1" {
		t.Fatalf("resolve direct: %q %v", d, err)
	}
	if s.Touched() > 2 {
		t.Fatalf("touched=%d", s.Touched())
	}
}
