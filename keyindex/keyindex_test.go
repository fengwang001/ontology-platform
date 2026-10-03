package keyindex

import (
	"testing"
)

func TestLoadSeekAndVersions(t *testing.T) {
	idx := New()
	idx.Load([]KeyVersions{
		{Key: "b", Versions: []Version{{ID: 1, IsDelete: false}}},
		{Key: "a", Versions: []Version{{ID: 3, IsDelete: false}, {ID: 2, IsDelete: true}, {ID: 1, IsDelete: false}}},
	})
	snap := idx.Snapshot()

	if got := snap.Len(); got != 2 {
		t.Fatalf("Len = %d, want 2", got)
	}
	key, vs, pos, ok := snap.SeekGE("a")
	if !ok || key != "a" || pos != 0 || len(vs) != 3 {
		t.Fatalf("SeekGE(a) = %q %v %d %v", key, vs, pos, ok)
	}
	if vs[0].ID != 3 || vs[2].ID != 1 {
		t.Fatalf("versions not desc: %+v", vs)
	}
	if idx.Seeks() != 1 {
		t.Fatalf("seeks = %d, want 1", idx.Seeks())
	}

	if _, _, _, ok := snap.SeekGE("z"); ok {
		t.Fatal("SeekGE(z) should miss")
	}
	if _, _, pos, ok := snap.SeekGE("a\x00"); !ok || pos != 1 {
		t.Fatalf("SeekGE(a\\x00) pos = %d ok=%v", pos, ok)
	}
	if idx.Seeks() != 3 {
		t.Fatalf("seeks = %d, want 3", idx.Seeks())
	}

	// At 不增加 seek。
	k2, _, ok := snap.At(1)
	if !ok || k2 != "b" {
		t.Fatalf("At(1) = %q %v", k2, ok)
	}
	if idx.Seeks() != 3 {
		t.Fatalf("At must not seek, got %d", idx.Seeks())
	}

	idx.ResetSeeks()
	if idx.Seeks() != 0 {
		t.Fatalf("ResetSeeks failed: %d", idx.Seeks())
	}
}

func TestPutMerge(t *testing.T) {
	idx := New()
	idx.Load([]KeyVersions{{Key: "a", Versions: []Version{{ID: 2, IsDelete: false}}}})
	idx.Put("a", Version{ID: 5, IsDelete: true})
	idx.Put("0", Version{ID: 1, IsDelete: false})
	snap := idx.Snapshot()

	if k, _, _, ok := snap.SeekGE(""); !ok || k != "0" {
		t.Fatalf("first key = %q", k)
	}
	_, vs, _, ok := snap.SeekGE("a")
	if !ok || len(vs) != 2 || vs[0].ID != 5 || !vs[0].IsDelete {
		t.Fatalf("merged versions = %+v", vs)
	}

	// 旧快照不可变。
	idx2 := New()
	idx2.Load(nil)
	idx2.Put("k", Version{ID: 1})
	old := idx2.Snapshot()
	idx2.Put("z", Version{ID: 2})
	if old.Len() != 1 {
		t.Fatalf("old snapshot mutated: len=%d", old.Len())
	}
}

func TestSucc(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"b/", "b0", true},
		{"a\xfe", "a\xff", true},
		{"\xff", "", false},
		{"x\xff\xff", "y", true},
		{"\x00\xff", "\x01", true},
		{"", "", false},
		{"abc", "abd", true},
	}
	for _, c := range cases {
		got, ok := Succ(c.in)
		if ok != c.ok || ok && got != c.want {
			t.Fatalf("Succ(%q) = %q,%v want %q,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}
