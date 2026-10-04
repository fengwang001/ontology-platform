package haircut

import (
	"errors"
	"testing"
)

func TestStdBondFloorPerBond(t *testing.T) {
	cases := []struct {
		n    int64
		rate int
		want int64
	}{
		{105, 90, 94}, // floor(94.5)
		{50, 75, 37},  // floor(37.5)
		{100, 90, 90}, // 取整边界
		{1, 0, 0},     // 零折算率
		{1, 150, 1},   // 折算率上限
		{10, 99, 9},   // floor(9.9)
		{101, 50, 50}, // 逐券取整：floor(50.5)
	}
	for _, c := range cases {
		if got := StdBond(c.n, c.rate); got != c.want {
			t.Errorf("StdBond(%d,%d)=%d want %d", c.n, c.rate, got, c.want)
		}
	}
	// 逐券取整与合并取整的差异：105@90 + 50@75
	perBond := StdBond(105, 90) + StdBond(50, 75)
	merged := (105*90 + 50*75) / 100
	if perBond != 131 || merged != 132 {
		t.Fatalf("per-bond=%d merged=%d, want 131 vs 132", perBond, merged)
	}
}

func TestOccupyCeil(t *testing.T) {
	cases := []struct{ amount, want int64 }{
		{13050, 131}, // ceil(130.5)
		{100, 1},
		{101, 2},
		{1, 1},
		{10000, 100},
	}
	for _, c := range cases {
		if got := Occupy(c.amount); got != c.want {
			t.Errorf("Occupy(%d)=%d want %d", c.amount, got, c.want)
		}
	}
}

func TestInterestCeil(t *testing.T) {
	// 13050 x 250bp x 7d: ceil(6.26)=7
	if got := Interest(13050, 250, 7); got != 7 {
		t.Fatalf("interest=%d want 7", got)
	}
	cases := []struct {
		amount int64
		r, d   int
		want   int64
	}{
		{3650000, 100, 365, 36500}, // 1%*1 年
		{1, 10000, 1, 1},           // ceil(1*10000/3650000)=1
		{365000, 0, 10, 0},         // 零利率
		{365000, 1, 10, 1},         // ceil(365000*10/3650000)=1
	}
	for _, c := range cases {
		if got := Interest(c.amount, c.r, c.d); got != c.want {
			t.Errorf("Interest(%d,%d,%d)=%d want %d", c.amount, c.r, c.d, got, c.want)
		}
	}
}

func TestValidation(t *testing.T) {
	if !ValidDay(0) || !ValidDay(1_000_000) || ValidDay(-1) || ValidDay(1_000_001) {
		t.Fatal("day bounds")
	}
	if !ValidRate(0) || !ValidRate(150) || ValidRate(151) || ValidRate(-1) {
		t.Fatal("rate bounds")
	}
	if !ValidPrice(1) || !ValidPrice(1_000_000) || ValidPrice(0) {
		t.Fatal("price bounds")
	}
	if !ValidQty(1) || !ValidQty(1_000_000_000_000) || ValidQty(0) {
		t.Fatal("qty bounds")
	}
	if !ValidRateBps(0) || !ValidRateBps(10000) || ValidRateBps(10001) {
		t.Fatal("bps bounds")
	}
	if NonEmpty(nil) || NonEmpty([]byte{}) || !NonEmpty([]byte("x")) {
		t.Fatal("nonempty")
	}
}

func TestMarketDuplicateAndMissing(t *testing.T) {
	m := NewMarket()
	if err := m.Add("A", 90, 99); err != nil {
		t.Fatal(err)
	}
	if err := m.Add("A", 80, 98); !errors.Is(err, ErrDupBond) {
		t.Fatalf("dup=%v", err)
	}
	if err := m.SetRate("X", 1); !errors.Is(err, ErrNoBond) {
		t.Fatalf("missing=%v", err)
	}
	if err := m.SetPrice("X", 1); !errors.Is(err, ErrNoBond) {
		t.Fatalf("missing=%v", err)
	}
	if err := m.SetRate("A", 89); err != nil {
		t.Fatal(err)
	}
	if err := m.SetPrice("A", 98); err != nil {
		t.Fatal(err)
	}
	if b, _ := m.Get("A"); b.Rate != 89 || b.Price != 98 {
		t.Fatalf("bond=%+v", b)
	}
}
