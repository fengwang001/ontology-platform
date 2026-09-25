package fire

import "testing"

func TestNextRateTable(t *testing.T) {
	cases := []struct {
		prevEnd, interval, want int64
	}{
		{0, 10, 0}, {3, 10, 10}, {25, 10, 30}, {30, 10, 30},
		{1, 1, 1}, {99, 7, 105}, {0, 5, 0}, {41, 10, 50},
	}
	for _, c := range cases {
		if got := NextRate(c.prevEnd, c.interval); got != c.want {
			t.Errorf("NextRate(%d,%d)=%d want %d", c.prevEnd, c.interval, got, c.want)
		}
	}
}

func TestNextDelayTable(t *testing.T) {
	cases := []struct{ prevEnd, interval, want int64 }{
		{3, 10, 13}, {28, 10, 38}, {0, 1, 1}, {100, 7, 107},
	}
	for _, c := range cases {
		if got := NextDelay(c.prevEnd, c.interval); got != c.want {
			t.Errorf("NextDelay(%d,%d)=%d want %d", c.prevEnd, c.interval, got, c.want)
		}
	}
}

// TestCheckedIsConstant 耗时覆盖 m 个 interval 时，检查个数不随 m 线性增长。
func TestCheckedIsConstant(t *testing.T) {
	for _, m := range []int64{100, 500, 1000, 5000, 10000} {
		prevEnd := m*10 + 5 // 上次执行覆盖了 m 个网格点
		if got := NextRate(prevEnd, 10); got != (m+1)*10 {
			t.Fatalf("m=%d NextRate=%d want %d", m, got, (m+1)*10)
		}
		if n := checked.Load(); n > 2 {
			t.Fatalf("m=%d checked=%d, 随 m 线性增长", m, n)
		}
	}
}
