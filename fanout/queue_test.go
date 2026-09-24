package fanout

import "testing"

// TestFIFOOrder 表驱动核验：FIFO 出队顺序、tail-drop、丢弃计数、长度，
// 含环形下标回绕场景。
func TestFIFOOrder(t *testing.T) {
	cases := []struct {
		name   string
		c      int
		offers []int64
		want   []int64 // 依次 Poll 到的全部事件
		drops  int
	}{
		{"cap1", 1, []int64{1, 2, 3}, []int64{1}, 2},
		{"cap2", 2, []int64{1, 2, 3, 4, 5}, []int64{1, 2}, 3},
		{"cap4-not-full", 4, []int64{10, 20}, []int64{10, 20}, 0},
		// c=3：放 1,2,3，取走 1,2，再放 4,5 触发回绕，剩余须为 [3,4,5]。
		{"ring-wrap", 3, nil, []int64{3, 4, 5}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := NewQueue(tc.c)
			if tc.name == "ring-wrap" {
				q.Offer(1)
				q.Offer(2)
				q.Offer(3)
				if v, ok := q.Poll(); !ok || v != 1 {
					t.Fatalf("first poll = (%d,%v), want (1,true)", v, ok)
				}
				if v, ok := q.Poll(); !ok || v != 2 {
					t.Fatalf("second poll = (%d,%v), want (2,true)", v, ok)
				}
				q.Offer(4)
				q.Offer(5)
			} else {
				for _, e := range tc.offers {
					q.Offer(e)
				}
			}
			var got []int64
			for {
				v, ok := q.Poll()
				if !ok {
					break
				}
				got = append(got, v)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("drained %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("drained %v, want %v", got, tc.want)
				}
			}
			if q.Dropped() != tc.drops {
				t.Fatalf("Dropped = %d, want %d", q.Dropped(), tc.drops)
			}
			if q.Len() != 0 {
				t.Fatalf("Len after drain = %d, want 0", q.Len())
			}
		})
	}
}

// TestEmptyPoll：空队列 Poll 返回 ok=false 且零值，状态不变。
func TestEmptyPoll(t *testing.T) {
	q := NewQueue(2)
	v, ok := q.Poll()
	if ok || v != 0 || q.Len() != 0 || q.Dropped() != 0 {
		t.Fatalf("empty poll = (%d,%v), len=%d dropped=%d", v, ok, q.Len(), q.Dropped())
	}
}

// TestSlotCheckBoundAcrossSizes 核验复杂度约束：容量 m 取 100..10000 多档，
// 一次 Offer 为判满检查的槽位数恒为 1（小常数），不随 m 增长。
func TestSlotCheckBoundAcrossSizes(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		q := NewQueue(m)
		before := q.slotChecks
		q.Offer(1)
		got := q.slotChecks - before
		if got != 1 {
			t.Fatalf("m=%d: slot checks per Offer = %d, want 1 (O(1), not slot scan)", m, got)
		}
		// 填满后再 Offer（tail-drop 路径），判满检查仍须是常数 1。
		for i := 1; i < m; i++ {
			q.Offer(int64(i + 1))
		}
		before = q.slotChecks
		q.Offer(999)
		if got := q.slotChecks - before; got != 1 {
			t.Fatalf("m=%d: slot checks on full Offer = %d, want 1", m, got)
		}
		if q.Dropped() != 1 {
			t.Fatalf("m=%d: Dropped = %d, want 1", m, q.Dropped())
		}
	}
}
