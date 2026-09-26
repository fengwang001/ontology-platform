package wfq

import "testing"

// TestHeapCheckBound 证明 Dequeue 用最小堆而非整表扫描定位最小完成时刻：
// 最近一次 Dequeue 检查的流个数（非导出字段 checked）不随 m 线性增长。
func TestHeapCheckBound(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		ws := make([]int, m)
		for i := range ws {
			ws[i] = 1
		}
		s, err := New(ws)
		if err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		for i := 0; i < m; i++ {
			if err := s.Submit(i, i+1); err != nil { // 完成时刻 1..m 互异
				t.Fatalf("m=%d submit: %v", m, err)
			}
		}
		got, err := s.Dequeue()
		if err != nil || got != 0 {
			t.Fatalf("m=%d: got flow %d, err %v", m, got, err)
		}
		if s.checked > 2 { // 与 m 无关的小常数：堆只需看堆顶
			t.Fatalf("m=%d: checked %d flows, grows with m", m, s.checked)
		}
	}
}
