package payout

import (
	"fmt"
	"testing"

	"ontology/revenue"
)

// buildHistory 制造 history 笔已付清的历史份额（全部出款并出列）。
func buildHistory(t *testing.T, l *Ledger, history int) int64 {
	t.Helper()
	for i := 0; i < history; i++ {
		if _, err := l.Earn(int64(i), fmt.Sprintf("h%d", i), "c", 1000); err != nil {
			t.Fatalf("Earn: %v", err)
		}
	}
	settleAt := int64(history) + 100
	if _, _, err := l.Settle(settleAt, "u"); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	return settleAt
}

// 一次 Settle 触碰的份额记录数不超过：本次被消耗（全部或部分）的份额数
// + 该创作者当前被冻结的份额数 + 未成熟份额数 + 2，与已付清历史份额数无关。
func TestSettleTouchedBound(t *testing.T) {
	for _, history := range []int{100, 10000} {
		t.Run(fmt.Sprintf("已付清%d笔", history), func(t *testing.T) {
			l, err := New(100, 500)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if err := l.SetSplit(0, "c", []revenue.Part{{Creator: "u", BPS: 10000}}); err != nil {
				t.Fatalf("SetSplit: %v", err)
			}
			settleAt := buildHistory(t, l, history)
			base := settleAt + 100
			// 2 笔将被冻结（入账在前，消耗循环需跳过）
			for _, id := range []string{"f1", "f2"} {
				if _, err := l.Earn(base, id, "c", 1000); err != nil {
					t.Fatalf("Earn %s: %v", id, err)
				}
			}
			if err := l.Hold(base+1, "hd", "c", base, base+1); err != nil {
				t.Fatalf("Hold: %v", err)
			}
			// 3 笔将成熟可用、2 笔将未成熟
			for _, id := range []string{"a1", "a2", "a3"} {
				if _, err := l.Earn(base+2, id, "c", 1000); err != nil {
					t.Fatalf("Earn %s: %v", id, err)
				}
			}
			for _, id := range []string{"i1", "i2"} {
				if _, err := l.Earn(base+3, id, "c", 1000); err != nil {
					t.Fatalf("Earn %s: %v", id, err)
				}
			}
			paid, _, err := l.Settle(base+102, "u")
			if err != nil {
				t.Fatalf("Settle: %v", err)
			}
			if paid != 3000 {
				t.Fatalf("paid=%d, want 3000", paid)
			}
			// 冻结跳过 2 + 消耗 3 = 5（left 归零即停，未成熟份额不被触碰）
			if l.touched != 5 {
				t.Fatalf("touched=%d, want 5", l.touched)
			}
			// 不变量上界：3(消耗) + 2(冻结) + 2(未成熟) + 2 = 9
			if l.touched > 9 {
				t.Fatalf("touched=%d 超过上界 9", l.touched)
			}
		})
	}
}

// 未达起付时剩余 available 份额不被触碰；与已付清历史份额数无关。
func TestSettleTouchedBelowMin(t *testing.T) {
	for _, history := range []int{100, 10000} {
		t.Run(fmt.Sprintf("已付清%d笔", history), func(t *testing.T) {
			l, err := New(0, 500)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if err := l.SetSplit(0, "c", []revenue.Part{{Creator: "u", BPS: 10000}}); err != nil {
				t.Fatalf("SetSplit: %v", err)
			}
			settleAt := buildHistory(t, l, history)
			base := settleAt + 10
			// 制造欠款 9600：入账 9600 → 出款 → 退款成欠款
			if _, err := l.Earn(base, "d", "c", 9600); err != nil {
				t.Fatalf("Earn: %v", err)
			}
			if _, _, err := l.Settle(base+1, "u"); err != nil {
				t.Fatalf("Settle: %v", err)
			}
			if err := l.Refund(base+2, "d"); err != nil {
				t.Fatalf("Refund: %v", err)
			}
			// 10 笔可用各 1000：A=10000，debt=9600，净额 400 < Min 不出款
			for i := 0; i < 10; i++ {
				if _, err := l.Earn(base+3+int64(i), fmt.Sprintf("e%d", i), "c", 1000); err != nil {
					t.Fatalf("Earn: %v", err)
				}
			}
			paid, offset, err := l.Settle(base+20, "u")
			if err != nil {
				t.Fatalf("Settle: %v", err)
			}
			if paid != 0 || offset != 9600 {
				t.Fatalf("paid=%d offset=%d, want 0/9600", paid, offset)
			}
			// 消耗 9 整 + 1 部分 = 10 份；剩余 1 份（400 分）不被触碰
			if l.touched != 10 {
				t.Fatalf("touched=%d, want 10（与已付清历史无关）", l.touched)
			}
			if bal := l.Balance("u", base+20); bal.Available != 400 {
				t.Fatalf("available=%d, want 400", bal.Available)
			}
		})
	}
}
