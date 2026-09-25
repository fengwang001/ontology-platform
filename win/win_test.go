package win

import "testing"

// TestCheckedUnitsConstant 证明 inflight/right 用计数器与边界指针 O(1)
// 维护：制造 m 字节在途后一次确认到底，最近一次操作检查的在途单元
// 个数不随 m 线性增长（不超过与 m 无关的小常数）。
func TestCheckedUnitsConstant(t *testing.T) {
	for _, m := range []int64{100, 500, 1000, 5000, 10000} {
		w := New(m)
		for i := int64(0); i < m; i++ {
			if !w.Sendable(1) {
				t.Fatalf("m=%d: byte %d not sendable", m, i)
			}
			w.AdvanceSend(1)
		}
		if !w.AckValid(m) {
			t.Fatalf("m=%d: ack to %d invalid", m, m)
		}
		w.ApplyAck(m)
		if w.checked > 2 {
			t.Errorf("m=%d: checked=%d, grows with inflight units", m, w.checked)
		}
		if w.una != m || w.right != 2*m {
			t.Errorf("m=%d: after full ack una=%d right=%d", m, w.una, w.right)
		}
	}
}
