package store

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"ontology/txid"
)

func put(t *testing.T, s *Store, key, val string) {
	t.Helper()
	tx := s.Begin()
	if err := tx.Put(key, []byte(val)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, v *View, key string) string {
	t.Helper()
	b, err := v.Get(key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	return string(b)
}

// 要求 1/2/3/4：快照隔离与可重复读、未提交与读己之写、回滚残留、
// 删除与从未存在可判定、快照点边界（左闭右开）。
func TestVisibility(t *testing.T) {
	t.Run("snapshot isolation repeatable", func(t *testing.T) {
		s := New(txid.NewCounter(), Config{})
		put(t, s, "a", "v1")
		v, _ := s.BeginView()
		defer v.Close()
		put(t, s, "a", "v2")
		for i := 0; i < 3; i++ {
			if got := get(t, v, "a"); got != "v1" {
				t.Fatalf("read %d = %q, want v1", i, got)
			}
		}
	})
	t.Run("uncommitted invisible own write visible", func(t *testing.T) {
		s := New(txid.NewCounter(), Config{})
		tx := s.Begin()
		_ = tx.Put("u", []byte("x"))
		v, _ := s.BeginView()
		defer v.Close()
		if _, err := v.Get("u"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("uncommitted visible: %v", err)
		}
		if got, err := tx.Get("u"); err != nil || string(got) != "x" {
			t.Fatalf("own write: %q %v", got, err)
		}
		_ = tx.Rollback()
	})
	t.Run("rollback leaves no residue", func(t *testing.T) {
		s := New(txid.NewCounter(), Config{})
		tx := s.Begin()
		_ = tx.Put("r", []byte("x"))
		_ = tx.Rollback()
		v, _ := s.BeginView()
		defer v.Close()
		if _, err := v.Get("r"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("rolled back visible: %v", err)
		}
		if st := s.Stats("r"); st.TotalVersions != 0 || st.KeyVersions != 0 {
			t.Fatalf("residue: %+v", st)
		}
	})
	t.Run("commit at snapshot point invisible", func(t *testing.T) {
		s := New(txid.NewCounter(), Config{})
		put(t, s, "b", "old")
		v, _ := s.BeginView() // point == 下一个将分配的事务号
		defer v.Close()
		put(t, s, "b", "new") // 该提交号恰好 == point
		if got := get(t, v, "b"); got != "old" {
			t.Fatalf("boundary read = %q, want old", got)
		}
	})
	t.Run("delete vs never existed", func(t *testing.T) {
		s := New(txid.NewCounter(), Config{})
		put(t, s, "keep", "y")
		put(t, s, "d", "x")
		tx := s.Begin()
		_ = tx.Delete("d")
		_ = tx.Commit()
		v, _ := s.BeginView()
		defer v.Close()
		cases := []struct {
			key  string
			want Status
		}{{"d", Deleted}, {"ghost", NeverExisted}, {"keep", Exists}}
		for _, c := range cases {
			if got := v.Status(c.key); got != c.want {
				t.Fatalf("Status(%q)=%v want %v", c.key, got, c.want)
			}
		}
		if _, err := v.Get("d"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleted Get = %v, want ErrNotFound", err)
		}
	})
}

// 要求 5：回收前后活跃快照读结果逐字节相同。
func TestReclaimPreservesReads(t *testing.T) {
	s := New(txid.NewCounter(), Config{})
	put(t, s, "g", "g1")
	v1, _ := s.BeginView()
	put(t, s, "g", "g2")
	v2, _ := s.BeginView()
	defer v2.Close()
	put(t, s, "g", "g3")
	v3, _ := s.BeginView()
	defer v3.Close()
	active := []*View{v2, v3}
	before := make([][]byte, len(active))
	for i, v := range active {
		b, err := v.Get("g")
		if err != nil {
			t.Fatal(err)
		}
		before[i] = b
	}
	v1.Close() // 释放最老快照，g1 变为可回收
	if n := s.Collect(); n != 1 {
		t.Fatalf("reclaimed %d versions, want 1", n)
	}
	for i, v := range active {
		after, err := v.Get("g")
		if err != nil || !bytes.Equal(before[i], after) {
			t.Fatalf("view %d changed: %q -> %q (%v)", i, before[i], after, err)
		}
	}
}

// 要求 7/10：水位只升不降、水位推过后新快照仍读到最新值、查询稳定。
func TestWatermarkAndStats(t *testing.T) {
	s := New(txid.NewCounter(), Config{})
	var last txid.T
	for i := 0; i < 6; i++ {
		put(t, s, "w", fmt.Sprintf("v%d", i))
		v, _ := s.BeginView()
		s.Collect()
		v.Close()
		s.Collect()
		if cur := s.Stats("w").Watermark; cur < last {
			t.Fatalf("watermark retreated: %d -> %d", last, cur)
		} else {
			last = cur
		}
	}
	v, _ := s.BeginView()
	defer v.Close()
	if got := get(t, v, "w"); got != "v5" {
		t.Fatalf("new snapshot read %q after reclaim", got)
	}
	if a, b := s.Stats("w"), s.Stats("w"); a != b {
		t.Fatalf("query not stable: %+v vs %+v", a, b)
	}
	if s.Stats("ghost").KeyVersions != 0 {
		t.Fatal("never existed key has versions")
	}
}

// 要求 6：单次回收考察数不随 N 增长（端到端，N=100 与 N=10000 对照）。
func TestIncrementalExamined(t *testing.T) {
	examined := func(n int) int {
		s := New(txid.NewCounter(), Config{})
		for i := 0; i < n; i++ {
			for j := 0; j < 3; j++ {
				put(t, s, fmt.Sprintf("k%06d", i), "v")
			}
		}
		s.Collect()
		base := s.Examined()
		for i := 0; i < 5; i++ {
			put(t, s, fmt.Sprintf("k%06d", i), "x")
		}
		s.Collect()
		return s.Examined() - base
	}
	e100, e10000 := examined(100), examined(10000)
	t.Logf("N=100 examined=%d, N=10000 examined=%d", e100, e10000)
	if e100 != e10000 {
		t.Fatalf("examined grows with N: %d vs %d", e100, e10000)
	}
}
