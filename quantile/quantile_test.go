package quantile

import (
	"errors"
	"math"
	"math/big"
	"math/bits"
	"testing"

	"ontology/merge"
	"ontology/schema"
)

// naiveRank 按规则 r=max(1,ceil(q*N/1000)) 用 128 位精确计算。
func naiveRank(q int, n uint64) uint64 {
	hi, lo := bits.Mul64(uint64(q), n)
	r := div128(hi, lo, 1000)
	if hi != 0 || lo%1000 != 0 {
		r++
	}
	return max(uint64(1), r)
}

func worked() schema.Hist {
	return schema.Hist{
		Name:   "lat",
		Bounds: []int64{20, 100},
		Counts: []uint64{6, 13, 3},
		Sum:    1200,
	}
}

func TestWorkedExampleQuantiles(t *testing.T) {
	h := worked()
	cases := []struct {
		q         int
		want      int64
		saturated bool
	}{
		{0, 3, false},    // r=1, floor(20*1/6)=3
		{500, 50, false}, // r=11, 20+floor(80*5/13)=50
		{950, 100, true}, // r=21 溢出桶
		{1000, 100, true},
	}
	for _, c := range cases {
		res, err := Quantile(h, c.q)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("q=%d r=%d -> value=%d saturated=%v（判定依据：%+v）", c.q, naiveRank(c.q, 22), res.Value, res.Saturated, h)
		if res.Value != c.want || res.Saturated != c.saturated {
			t.Fatalf("Quantile(%d)=(%d,%v), want (%d,%v)", c.q, res.Value, res.Saturated, c.want, c.saturated)
		}
	}
}

func TestRankRounding(t *testing.T) {
	for _, c := range [][3]uint64{{950, 22, 21}, {500, 22, 11}, {0, 22, 1}, {1000, 22, 22}} {
		if r := naiveRank(int(c[0]), c[1]); r != c[2] {
			t.Fatalf("naiveRank(%d,%d)=%d want %d", c[0], c[1], r, c[2])
		}
	}
}

func TestErrors(t *testing.T) {
	h := worked()
	for _, q := range []int{-1, 1001} {
		if _, err := Quantile(h, q); !errors.Is(err, schema.ErrInvalidArgument) {
			t.Fatalf("q=%d 应参数非法: %v", q, err)
		}
	}
	bad := h
	bad.Counts = []uint64{0, 0}
	if _, err := Quantile(bad, 500); !errors.Is(err, schema.ErrInvalidArgument) {
		t.Fatalf("非法 Hist 同为参数非法: %v", err)
	}
	empty := h
	empty.Counts = []uint64{0, 0, 0}
	if _, err := Quantile(empty, 0); !errors.Is(err, schema.ErrEmpty) {
		t.Fatalf("空直方图应空错误: %v", err)
	}
}

func TestMonotonic(t *testing.T) {
	h := worked()
	var prev int64
	for q := 0; q <= 1000; q++ {
		res, err := Quantile(h, q)
		if err != nil {
			t.Fatal(err)
		}
		if res.Value < prev {
			t.Fatalf("q=%d 值 %d 小于前值 %d，分位数必须单调不减", q, res.Value, prev)
		}
		prev = res.Value
	}
}

func TestInterpolationFloorDirection(t *testing.T) {
	// 桶 (0,10] 计数 3：q 值在桶内时 floor(10*w/3) 向下取整，序列 3,6,10。
	h := schema.Hist{Name: "f", Bounds: []int64{10}, Counts: []uint64{3, 0}, Sum: 0}
	want := map[int]int64{1: 3, 2: 6, 3: 10}
	for w, v := range want {
		q := w * 1000 / 3 // 使秩恰为 w：333、666、1000
		res, err := Quantile(h, q)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("秩 w=%d (q=%d) -> %d（floor 方向，期望 %d）", w, q, res.Value, v)
		if res.Value != v {
			t.Fatalf("w=%d got %d want %d", w, res.Value, v)
		}
	}
}

func Test128BitProduct(t *testing.T) {
	// 宽度与 w 的乘积超过 64 位：hi=1e12-1, w=9e18，c=MaxInt64。
	width := uint64(1_000_000_000_000 - 1)
	w := uint64(9_000_000_000_000_000_000)
	c := uint64(math.MaxInt64)
	phi, plo := bits.Mul64(width, w)
	got := div128(phi, plo, c)
	// 参考：math/big
	prod := new(big.Int).Mul(big.NewInt(int64(width)), new(big.Int).SetUint64(w))
	want := new(big.Int).Div(prod, new(big.Int).SetInt64(math.MaxInt64))
	if got != want.Uint64() {
		t.Fatalf("128 位乘积除法 got %d want %s", got, want)
	}
	t.Logf("128 位插值乘积 width=%d*w=%d/c=%d = %d（hi=%d lo=%d）", width, w, c, got, phi, plo)
}

func TestSaturatedReturnsLastBound(t *testing.T) {
	h := schema.Hist{Name: "s", Bounds: []int64{5, 99}, Counts: []uint64{0, 1, 5}, Sum: 0}
	res, err := Quantile(h, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Saturated || res.Value != 99 {
		t.Fatalf("溢出桶应返回最后边界 99 并标记 Saturated, got %+v", res)
	}
}

// TestEndToEndExample 走完整链路：登记版本链 → 分版本采集 → 跨版本合并 → 分位数。
func TestEndToEndExample(t *testing.T) {
	reg := schema.NewRegistry()
	v1, err := reg.Register("lat", []int64{10, 20, 50, 100})
	if err != nil || v1 != 1 {
		t.Fatal(err)
	}
	v2, err := reg.Register("lat", []int64{20, 100})
	if err != nil || v2 != 2 {
		t.Fatal(err)
	}
	ca, _ := schema.NewCollector(reg, "lat", v1)
	for _, v := range []int64{0, 10, 10, 20, 20, 21, 30, 49, 50, 100} {
		if err := ca.Observe(v); err != nil {
			t.Fatal(err)
		}
	}
	cb, _ := schema.NewCollector(reg, "lat", v2)
	for _, v := range []int64{20, 21, 30, 40, 50, 60, 70, 80, 100, 101, 200, 300} {
		if err := cb.Observe(v); err != nil {
			t.Fatal(err)
		}
	}
	ha, hb := ca.Snapshot(), cb.Snapshot()
	t.Logf("采集 a counts=%v sum=%d；b counts=%v sum=%d", ha.Counts, ha.Sum, hb.Counts, hb.Sum)
	wantAC, wantBC := []uint64{3, 2, 4, 1, 0}, []uint64{1, 8, 3}
	for i, c := range wantAC {
		if ha.Counts[i] != c {
			t.Fatalf("a 桶%d=%d want %d", i, ha.Counts[i], c)
		}
	}
	for i, c := range wantBC {
		if hb.Counts[i] != c {
			t.Fatalf("b 桶%d=%d want %d", i, hb.Counts[i], c)
		}
	}
	m, err := merge.Merge(ha, hb)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("合并=%+v（N=22，期望 counts [6 13 3]）", m)
	for i, c := range []uint64{6, 13, 3} {
		if m.Counts[i] != c {
			t.Fatalf("合并桶%d=%d want %d", i, m.Counts[i], c)
		}
	}
	if m.Sum != ha.Sum+hb.Sum {
		t.Fatal("合并 Sum 不守恒")
	}
	q500, _ := Quantile(m, 500)
	q950, _ := Quantile(m, 950)
	q0, _ := Quantile(m, 0)
	t.Logf("分位数 q=0 -> %d, q=500 -> %d, q=950 -> %+v", q0.Value, q500.Value, q950)
	if q0.Value != 3 || q500.Value != 50 || q950.Value != 100 || !q950.Saturated {
		t.Fatalf("端到端分位数不符: %d %d %+v", q0.Value, q500.Value, q950)
	}
}
