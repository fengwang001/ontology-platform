package refund

import (
	"errors"
	"math/big"
	"testing"
)

func TestNewInvalid(t *testing.T) {
	for _, c := range [][2]int64{{-1, 0}, {101, 0}, {0, -1}, {0, 1_000_000_001}} {
		if _, err := New(c[0], c[1]); !errors.Is(err, ErrInvalid) {
			t.Fatalf("New(%d,%d) err=%v want ErrInvalid", c[0], c[1], err)
		}
	}
}

func refBig(paid, shipped, a, b, beta int64) int64 {
	num := new(big.Int).Mul(big.NewInt(paid),
		new(big.Int).Add(new(big.Int).Mul(big.NewInt(100), big.NewInt(a)),
			new(big.Int).Mul(big.NewInt(beta), big.NewInt(b))))
	den := new(big.Int).Mul(big.NewInt(100), big.NewInt(shipped))
	return new(big.Int).Quo(num, den).Int64()
}

func TestCumulativeTable(t *testing.T) {
	c, err := New(80, 50)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		paid, shipped, a, b, want int64
	}{
		{1000, 3, 1, 0, 333},  // 题例 t=7
		{1000, 3, 1, 1, 600},  // 题例 t=8
		{1000, 3, 2, 1, 933},  // 题例末件
		{1000, 3, 3, 0, 1000}, // 全 A 收回恰等于 paid
		{1000, 3, 0, 3, 800},  // 全 B 收回恰等于 floor(β%)
		{0, 5, 5, 0, 0},       // paid=0
		{1_000_000_000_000, 1_000_000, 1_000_000, 0, 1_000_000_000_000}, // 上限不溢出、恰等
		{1_000_000_000_000, 1_000_000, 0, 1_000_000, 800_000_000_000},   // 全 B 上限
		{1_000_000_000_000, 1_000_000, 500_001, 499_999, 900_000_200_000},
	}
	for _, tc := range cases {
		got := c.Cumulative(tc.paid, tc.shipped, tc.a, tc.b)
		wantBig := refBig(tc.paid, tc.shipped, tc.a, tc.b, 80)
		if tc.want != wantBig {
			t.Fatalf("case fixture wrong: got big %d want %d", wantBig, tc.want)
		}
		if got != tc.want {
			t.Errorf("Cumulative(paid=%d sh=%d a=%d b=%d)=%d want %d",
				tc.paid, tc.shipped, tc.a, tc.b, got, tc.want)
		}
	}
}

func TestCumulative128Bound(t *testing.T) {
	// 中间积 1e12 * 1e8 = 1e20，超过 64 位；与 big.Int 对拍一批。
	c, _ := New(100, 0)
	for _, a := range []int64{1, 777_777, 999_999, 1_000_000} {
		got := c.Cumulative(1_000_000_000_000, 1_000_000, a, 0)
		want := refBig(1_000_000_000_000, 1_000_000, a, 0, 100)
		if got != want {
			t.Fatalf("a=%d got=%d big=%d", a, got, want)
		}
		if got > 1_000_000_000_000 {
			t.Fatalf("refund exceeds paid: %d", got)
		}
	}
}

func TestSettleFeeAcrossReceipts(t *testing.T) {
	c, _ := New(100, 50)
	c.Open("R")
	// 应退小于欠额：分次扣完
	if fee, paid := c.Settle("R", 20); fee != 20 || paid != 0 || c.Owe("R") != 30 {
		t.Fatalf("first settle fee=%d paid=%d owe=%d", fee, paid, c.Owe("R"))
	}
	if fee, paid := c.Settle("R", 20); fee != 20 || paid != 0 || c.Owe("R") != 10 {
		t.Fatalf("second settle fee=%d paid=%d owe=%d", fee, paid, c.Owe("R"))
	}
	if fee, paid := c.Settle("R", 30); fee != 10 || paid != 20 || c.Owe("R") != 0 {
		t.Fatalf("third settle fee=%d paid=%d owe=%d", fee, paid, c.Owe("R"))
	}
	// 欠额已清：后续不再扣费
	if fee, paid := c.Settle("R", 100); fee != 0 || paid != 100 {
		t.Fatalf("after clear fee=%d paid=%d", fee, paid)
	}
	// 应退为 0 不扣费
	c.Open("R2")
	if fee, paid := c.Settle("R2", 0); fee != 0 || paid != 0 || c.Owe("R2") != 50 {
		t.Fatalf("zero due changed owe: fee=%d paid=%d owe=%d", fee, paid, c.Owe("R2"))
	}
	// 到期作废剩余欠额
	c.Void("R2")
	if c.Owe("R2") != 0 {
		t.Fatalf("voided owe=%d", c.Owe("R2"))
	}
}
