package booking

import (
	"fmt"
	"testing"

	"ontology/slotpool"
)

// TestTouchedBounded 证明两个非导出计数器与预约总数无关：
//  1. 禁约判定读取的爽约记录不超过 K 条；
//  2. 一次落地循环从到期堆取出的预约数不超过实际落地数 + 1。
//
// 对照 1000 与 100000 两档，两档的 touched 上界相同。
func TestTouchedBounded(t *testing.T) {
	for _, nSlots := range []int{1000, 100000} {
		name := fmt.Sprintf("slots=%d", nSlots)
		t.Run(name, func(t *testing.T) {
			// K=3：credit.Banned 无论历史记录多少，读取条数 ≤ 3。
			s := New(0, 0, 0, 0, 3, 10000000)
			// 每个槽 1 个线上号，start=10；dead=10，now=11 全部爽约。
			for i := 0; i < nSlots; i++ {
				id := fmt.Sprintf("s%d", i)
				if err := s.AddSlot(0, id, 10, 1, 1); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < nSlots; i++ {
				id := fmt.Sprintf("s%d", i)
				p := []byte(fmt.Sprintf("p%d", i))
				if _, err := s.Book(1, p, id, slotpool.Online); err != nil {
					t.Fatal(err)
				}
			}
			if s.due.Len() != nSlots {
				t.Fatalf("due heap size = %d want %d", s.due.Len(), nSlots)
			}
			// now=10：dead=10 恰等不到期，不落地；堆顶探测 1 次，取出 0、落地 0。
			if err := s.CheckIn(10, []byte("p0"), "s0"); err != nil {
				t.Fatalf("checkin at dead boundary: %v", err)
			}
			if s.landTaken != 0 || s.landApplied != 0 || s.landProbed != 1 {
				t.Fatalf("now=10 taken=%d applied=%d probed=%d",
					s.landTaken, s.landApplied, s.landProbed)
			}
			// now=11：其余 nSlots-1 条全部到期，一次操作完成全部落地。
			if err := s.CheckIn(11, []byte("pX"), "s0"); err != ErrNoBooking {
				t.Fatalf("got %v want ErrNoBooking", err)
			}
			applied := nSlots - 1
			if s.landApplied != applied {
				t.Fatalf("applied = %d want %d", s.landApplied, applied)
			}
			if s.landTaken != applied {
				t.Fatalf("taken = %d want applied=%d (应等于落地数，无额外取出)",
					s.landTaken, applied)
			}
			if s.landProbed != 0 {
				t.Fatalf("probed = %d want 0 (全部落地后无额外探测)", s.landProbed)
			}
			if s.due.Len() != 0 {
				t.Fatalf("due heap should be empty, got %d", s.due.Len())
			}
			// 禁约判定：某患者只有 1 条，读取 1 条即遇出窗终止；≤K。
			s.Banned(11, []byte("p1"))
			if got := s.cred.Touched(); got > 3 {
				t.Fatalf("credit touched = %d > K=3", got)
			}
			if got := s.cred.Touched(); got != 1 {
				t.Fatalf("credit touched = %d want 1", got)
			}
		})
	}
}

// TestTouchedCreditIndependentOfHistory 单独证明禁约判定读取与历史记录数无关：
// 给同一患者灌入 100000 条出窗记录 + 3 条窗内记录，读取条数恰为 3。
func TestTouchedCreditIndependentOfHistory(t *testing.T) {
	k, w := int64(3), int64(1000)
	s := New(0, 0, 0, 0, int(k), int(w))
	victim := []byte("v")
	for i := 0; i < 100000; i++ {
		s.cred.Add("v", 100) // 全部远早于 now-w
	}
	for i := 0; i < 3; i++ {
		s.cred.Add("v", 5000)
	}
	if !s.Banned(5001, victim) {
		t.Fatal("应禁约")
	}
	if got := s.cred.Touched(); got != int(k) {
		t.Fatalf("touched = %d want %d（与 100000 条历史无关）", got, k)
	}
}
