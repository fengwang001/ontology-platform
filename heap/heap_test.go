package heap

import "testing"

// TestFindMinComparisonsConstant：k 取多档规模、count 互不相同，
// 替换中“找最小 count 计数器”的比较次数恒为小常数，不随 k 线性增长。
func TestFindMinComparisonsConstant(t *testing.T) {
	for _, k := range []int{100, 500, 1000, 5000, 10000} {
		h := New()
		for i := 0; i < k; i++ {
			h.Add(Counter{Key: i, Count: i + 1}) // 互不相同的 count
		}
		old := h.Min() // 替换的“找最小”步骤
		h.ReplaceMin(Counter{Key: k, Count: old.Count + 1, Err: old.Count})
		if h.findCmp > 2 {
			t.Fatalf("k=%d: 找最小比较次数 = %d，应恒为小常数（线性扫应约为 %d）", k, h.findCmp, k-1)
		}
		if old.Count != 1 || old.Key != 0 {
			t.Fatalf("k=%d: 最小计数器应为 (0,1,0)，得到 %+v", k, old)
		}
	}
}

// TestMinTieBreakByKey：count 并列时 Min 返回 Key 最小者（表驱动）。
func TestMinTieBreakByKey(t *testing.T) {
	cases := []struct {
		name    string
		keys    []int // 全部 count=1，制造并列
		wantKey int
	}{
		{"两个并列", []int{7, 3}, 3},
		{"乱序多个", []int{9, 4, 4 + 1, 2, 8}, 2},
		{"插入顺序无关", []int{5, 1, 6, 0, 3}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := New()
			for _, key := range tc.keys {
				h.Add(Counter{Key: key, Count: 1})
			}
			if got := h.Min().Key; got != tc.wantKey {
				t.Fatalf("Min().Key = %d，want %d", got, tc.wantKey)
			}
		})
	}
}

// TestIncAndReplace：增与替换后堆序仍正确（表驱动逐步断言）。
func TestIncAndReplace(t *testing.T) {
	h := New()
	h.Add(Counter{Key: 1, Count: 1})
	h.Add(Counter{Key: 2, Count: 1})
	h.Add(Counter{Key: 3, Count: 1})
	h.Inc(3) // (3,2,0)
	if got := h.Min().Key; got != 1 {
		t.Fatalf("Inc 后 Min().Key = %d，want 1", got)
	}
	old := h.Min()
	h.ReplaceMin(Counter{Key: 4, Count: old.Count + 1, Err: old.Count}) // 踢 1
	if _, ok := h.Get(1); ok {
		t.Fatal("Key 1 应已被替换")
	}
	if got := h.Min().Key; got != 2 {
		t.Fatalf("替换后 Min().Key = %d，want 2", got)
	}
	if c, _ := h.Get(4); c.Count != 2 || c.Err != 1 {
		t.Fatalf("新计数器 = %+v，want (4,2,1)", c)
	}
}
