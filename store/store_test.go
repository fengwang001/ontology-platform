package store

import "testing"

// TestApplyTailAppendConstant 证明 Apply 是纯尾部追加：先追加 m 条 delta，
// 再 Apply 一条新 delta，其访问的已有条目数不随 m 线性增长。
// lastVisited 是非导出字段，仅同包测试可读，不经任何导出接口暴露。
func TestApplyTailAppendConstant(t *testing.T) {
	const bound = 4 // 与 m 无关的小常数上界
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		s, err := New(m + 1)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < m; i++ {
			if err := s.Apply("k", int64(i)); err != nil {
				t.Fatalf("m=%d fill: %v", m, err)
			}
		}
		if err := s.Apply("k", 1); err != nil {
			t.Fatalf("m=%d append: %v", m, err)
		}
		if s.lastVisited > bound {
			t.Fatalf("m=%d: visited %d existing entries, want <= %d (O(1) tail append)",
				m, s.lastVisited, bound)
		}
		if got := s.DeltaCount(); got != m+1 {
			t.Fatalf("m=%d: DeltaCount=%d, want %d", m, got, m+1)
		}
	}
}

// TestStoreTable 表驱动：多 Key 交错操作下的 Get/Compact/DeltaCount 行为。
func TestStoreTable(t *testing.T) {
	type op struct {
		key   string
		delta int64
	}
	cases := []struct {
		name  string
		ops   []op
		want  map[string]int64
		count int
	}{
		{"empty", nil, map[string]int64{"x": 0}, 0},
		{"single", []op{{"a", 5}}, map[string]int64{"a": 5, "b": 0}, 1},
		{"neg-zero", []op{{"a", 3}, {"a", -3}, {"a", 0}}, map[string]int64{"a": 0}, 3},
		{"multi-key", []op{{"a", 1}, {"b", 2}, {"a", -1}}, map[string]int64{"a": 0, "b": 2}, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := New(64)
			if err != nil {
				t.Fatal(err)
			}
			for _, o := range tc.ops {
				if err := s.Apply(o.key, o.delta); err != nil {
					t.Fatal(err)
				}
			}
			for k, w := range tc.want {
				if got := s.Get(k); got != w {
					t.Fatalf("Get(%q)=%d, want %d", k, got, w)
				}
			}
			if got := s.DeltaCount(); got != tc.count {
				t.Fatalf("DeltaCount=%d, want %d", got, tc.count)
			}
			// Compact 前后可见值不变，之后 DeltaCount 归零
			before := map[string]int64{}
			for k := range tc.want {
				before[k] = s.Get(k)
			}
			s.Compact()
			for k, w := range before {
				if got := s.Get(k); got != w {
					t.Fatalf("after Compact Get(%q)=%d, want %d", k, got, w)
				}
			}
			if got := s.DeltaCount(); got != 0 {
				t.Fatalf("after Compact DeltaCount=%d, want 0", got)
			}
		})
	}
}

// TestStoreRejections 表驱动：三类拒绝互不相同且不留痕。
func TestStoreRejections(t *testing.T) {
	if _, err := New(0); err != ErrBadMax {
		t.Fatalf("New(0) err=%v, want ErrBadMax", err)
	}
	if _, err := New(-3); err != ErrBadMax {
		t.Fatalf("New(-3) err=%v, want ErrBadMax", err)
	}
	s, err := New(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Apply("", 1); err != ErrEmptyKey {
		t.Fatalf("empty key err=%v, want ErrEmptyKey", err)
	}
	if err := s.Apply("a", 7); err != nil {
		t.Fatal(err)
	}
	if err := s.Apply("b", 1); err != ErrCapacity {
		t.Fatalf("over capacity err=%v, want ErrCapacity", err)
	}
	if ErrEmptyKey == ErrCapacity || ErrEmptyKey == ErrBadMax || ErrCapacity == ErrBadMax {
		t.Fatal("sentinel errors must be distinct")
	}
	if got := s.Get("a"); got != 7 {
		t.Fatalf("after rejections Get(a)=%d, want 7", got)
	}
	if got := s.DeltaCount(); got != 1 {
		t.Fatalf("after rejections DeltaCount=%d, want 1", got)
	}
}
