package slot

import "testing"

// TestOpenCostLogBound：多档 C，反复 Open/Close/Open 制造大量复用，
// 断言每次 Open 定位最小空闲槽检查的槽数 ≤ ⌈log2(C)⌉+1（对数而非线性）。
func TestOpenCostLogBound(t *testing.T) {
	for _, C := range []int{100, 500, 1000, 5000, 10000} {
		tab := New(C)
		var hs []Handle
		// 先占满前 90% 的槽
		for i := 0; i < C*9/10; i++ {
			h, err := tab.Open()
			if err != nil {
				t.Fatalf("C=%d open %d: %v", C, i, err)
			}
			hs = append(hs, h)
		}
		// 关掉一半，再逐个重开，制造大量复用；每次 Open 后核验代价
		for r := 0; r < 3; r++ {
			var closed []Handle
			for i := 0; i < len(hs); i += 2 {
				if _, ok := tab.Close(hs[i]); !ok {
					t.Fatalf("C=%d close %+v failed", C, hs[i])
				}
				closed = append(closed, hs[i])
			}
			hs = hs[:0]
			for range closed {
				if _, err := tab.Open(); err != nil {
					t.Fatalf("C=%d reopen: %v", C, err)
				}
				if bound := ceilLog2(C) + 1; tab.checked > bound {
					t.Fatalf("C=%d: open checked %d slots > bound %d (linear scan?)", C, tab.checked, bound)
				}
			}
			// 重新占满前 90%
			hs = hs[:0]
			for i := 0; i < C*9/10; i++ {
				h, err := tab.Open()
				if err != nil {
					break
				}
				hs = append(hs, h)
			}
		}
	}
}

// TestSlotRules 表驱动：最小空闲分配、世代只在 Open 递增、Close 不归零、句柄校验。
func TestSlotRules(t *testing.T) {
	tab := New(3)
	h0, _ := tab.Open()
	h1, _ := tab.Open()
	h2, _ := tab.Open()
	if h0 != (Handle{0, 1}) || h1 != (Handle{1, 1}) || h2 != (Handle{2, 1}) {
		t.Fatalf("handles: %+v %+v %+v", h0, h1, h2)
	}
	if _, err := tab.Open(); err != ErrNoSlots {
		t.Fatalf("want ErrNoSlots, got %v", err)
	}
	if _, ok := tab.Close(Handle{1, 2}); ok { // 世代不符
		t.Fatal("close with wrong gen should fail")
	}
	if _, ok := tab.Close(Handle{9, 1}); ok { // 越界
		t.Fatal("close with bad id should fail")
	}
	if _, ok := tab.Close(h1); !ok {
		t.Fatal("close h1 failed")
	}
	if _, gen, _ := tab.State(1); gen != 1 { // gen 不归零
		t.Fatalf("gen reset to %d", gen)
	}
	h, err := tab.Open() // 复用最小空闲 1，gen 1→2
	if err != nil || h != (Handle{1, 2}) {
		t.Fatalf("reuse: %+v %v", h, err)
	}
}
