package ver

import "testing"

// naive 是对版本切片按定义朴素扫描的参考实现。
type naive struct{ vs []Version }

func (n naive) latestEvent() Version {
	b := n.vs[0]
	for _, v := range n.vs[1:] {
		if v.Ev > b.Ev || (v.Ev == b.Ev && v.In > b.In) {
			b = v
		}
	}
	return b
}

func (n naive) atEvent(t int64) (Version, bool) {
	b, ok := Version{}, false
	for _, v := range n.vs {
		if v.Ev <= t && (!ok || v.Ev > b.Ev || (v.Ev == b.Ev && v.In > b.In)) {
			b, ok = v, true
		}
	}
	return b, ok
}

func (n naive) atIngest(t int64) (Version, bool) {
	b, ok := Version{}, false
	for _, v := range n.vs {
		if v.In <= t && (!ok || v.In > b.In) {
			b, ok = v, true
		}
	}
	return b, ok
}

// TestMatchesNaiveScan 不变量1：任意 Apply 序列后四类查询都等于朴素扫描。
func TestMatchesNaiveScan(t *testing.T) {
	cases := []struct {
		name   string
		n, mod int
	}{
		{"小", 7, 3}, {"中", 200, 50}, {"大-乱序", 2000, 997}, {"同Ev并列", 500, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := &History{}
			var n naive
			for i := 0; i < c.n; i++ { // 乱序 Ev（含负值与并列），In 严格递增
				v := Version{Value: "v", Ev: int64((i*7919+13)%(c.mod+1) - c.mod/2), In: int64(i + 1)}
				h.Append(v)
				n.vs = append(n.vs, v)
				if got, _ := h.LatestEvent(); got != n.latestEvent() {
					t.Fatalf("i=%d LatestEvent=%+v, 朴素=%+v", i, got, n.latestEvent())
				}
				if got, _ := h.LatestIngest(); got != v {
					t.Fatalf("i=%d LatestIngest=%+v, 朴素=%+v", i, got, v)
				}
				for _, ts := range []int64{-100, -1, 0, v.Ev, 100} {
					if got, err := h.AtEvent(ts); err != nil {
						if _, ok := n.atEvent(ts); ok {
							t.Fatalf("i=%d AtEvent(%d) 误报不存在", i, ts)
						}
					} else if want, _ := n.atEvent(ts); got != want {
						t.Fatalf("i=%d AtEvent(%d)=%+v, 朴素=%+v", i, ts, got, want)
					}
					if got, err := h.AtIngest(ts); err != nil {
						if _, ok := n.atIngest(ts); ok {
							t.Fatalf("i=%d AtIngest(%d) 误报不存在", i, ts)
						}
					} else if want, _ := n.atIngest(ts); got != want {
						t.Fatalf("i=%d AtIngest(%d)=%+v, 朴素=%+v", i, ts, got, want)
					}
				}
			}
		})
	}
}

// TestLatestEventMonotonic 不变量2：LatestEvent 的 Ev 随 Apply 单调不减。
func TestLatestEventMonotonic(t *testing.T) {
	h := &History{}
	prev := int64(-1) << 62
	for i := 0; i < 1000; i++ {
		h.Append(Version{Value: "v", Ev: int64((i*104729 + 7) % 601), In: int64(i + 1)})
		got, err := h.LatestEvent()
		if err != nil || got.Ev < prev {
			t.Fatalf("i=%d Ev 回退: %v -> %v", i, prev, got.Ev)
		}
		prev = got.Ev
	}
}

// TestLatestEventIsO1 复杂度：checked 不随版本数 m 增长（指针 O(1)，非全表扫描）。
func TestLatestEventIsO1(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		h := &History{}
		for i := 0; i < m; i++ {
			h.Append(Version{Value: "v", Ev: int64((i*7919 + 13) % 4999), In: int64(i + 1)})
		}
		if _, err := h.LatestEvent(); err != nil {
			t.Fatal(err)
		}
		if h.checked > 1 {
			t.Fatalf("m=%d 时检查了 %d 个版本，随 m 线性增长", m, h.checked)
		}
	}
}

// TestNotFound 空历史四类查询一律 ErrNotFound。
func TestNotFound(t *testing.T) {
	var h *History // nil 与空历史同语义
	if _, err := h.LatestEvent(); err != ErrNotFound {
		t.Fatal(err)
	}
	if _, err := (&History{}).LatestIngest(); err != ErrNotFound {
		t.Fatal(err)
	}
	if _, err := h.AtEvent(0); err != ErrNotFound {
		t.Fatal(err)
	}
	if _, err := h.AtIngest(99); err != ErrNotFound {
		t.Fatal(err)
	}
}
