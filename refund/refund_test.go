package refund

import (
	"errors"
	"testing"
)

func TestNewValidation(t *testing.T) {
	cases := []struct {
		name    string
		beta    int64
		wantErr error
	}{
		{"zero", 0, nil},
		{"full", 100, nil},
		{"neg", -1, ErrInvalid},
		{"over", 101, ErrInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.beta)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
		})
	}
}

func TestCumulative(t *testing.T) {
	c, _ := New(80)
	cases := []struct {
		name                string
		paid, shipped, a, b int64
		want                int64
	}{
		// 例：1000 分 / 3 件，β=80。
		{"first A", 1000, 3, 1, 0, 333},              // floor(1000*100/300)
		{"A then B", 1000, 3, 1, 1, 600},             // floor(1000*180/300)
		{"second B delta basis", 1000, 3, 1, 2, 866}, // floor(1000*260/300)
		{"all A equals paid", 1000, 3, 3, 0, 1000},   // 全行 A：恰等于 paid
		{"all B", 1000, 3, 0, 3, 800},
		{"none", 1000, 3, 0, 0, 0},
		{"beta zero all A", 1, 1, 1, 0, 1},
		{"floor below unit", 1, 2, 0, 1, 0}, // floor(1*80/200)=0
		// paid、shipped 取上限，中间名义值 1e12*1e4=1e16（要求覆盖到 1e20 场景的等价 128 位）。
		{"max values all A", 1_000_000_000_000, 1_000_000, 1_000_000, 0, 1_000_000_000_000},
		{"max values one A", 1_000_000_000_000, 1_000_000, 1, 0, 1_000_000},
		{"max values one B", 1_000_000_000_000, 1_000_000, 0, 1, 800_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.Cumulative(tc.paid, tc.shipped, tc.a, tc.b); got != tc.want {
				t.Fatalf("Cumulative=%d want %d", got, tc.want)
			}
		})
	}
}

func TestDeltaNotPerItemRounding(t *testing.T) {
	// 累计差额：第 2 件(B) 应退 = 600-333 = 267，逐件取整只得 266。
	c, _ := New(80)
	first := c.Cumulative(1000, 3, 1, 0)
	second := c.Cumulative(1000, 3, 1, 1) - first
	if first != 333 || second != 267 {
		t.Fatalf("first=%d second=%d, want 333/267", first, second)
	}
}

func Test128BitRange(t *testing.T) {
	// shipped 上限 1e6：系数 100*a 可达 1e8；乘 paid 1e12 = 1e20，超出 int64，仍须精确 floor。
	c, _ := New(100)
	got := c.Cumulative(1_000_000_000_000, 1_000_000, 1_000_000, 0)
	if got != 1_000_000_000_000 {
		t.Fatalf("1e20-range Cumulative=%d want paid", got)
	}
}
