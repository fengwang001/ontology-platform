package res

import (
	"math/rand"
	"testing"
)

// 钉住复杂度约束：m 个互不重叠的已激活预留，在空隙处 CanAdd，
// 扫描阶段检查的预留个数不随 m 增长（与 m 无关的小常数）。
// checked 是非导出字段，本测试与同包，直接读字段，不经任何导出接口。
func TestCheckedBounded(t *testing.T) {
	for _, m := range []int64{100, 1000, 10000} {
		c := NewCore(1 << 40)
		for i := int64(0); i < m; i++ {
			c.Add(i*4, i*4+2, 1) // [0,2) [4,6) [8,10) … 互不重叠
		}
		if !c.CanAdd(2, 3, 1) { // 落在空隙 [2,4) 内
			t.Fatalf("m=%d: gap reserve should fit", m)
		}
		if c.checked > 2 {
			t.Fatalf("m=%d: checked=%d grows with m", m, c.checked)
		}
	}
}

// 表驱动：Peak 与朴素逐点扫描一致，含左闭右开边界。
func TestPeakTable(t *testing.T) {
	cases := []struct {
		ivs      [][3]int64
		s, e     int64
		wantPeak int64
	}{
		{nil, 0, 5, 0},
		{[][3]int64{{0, 5, 4}}, 0, 5, 4},
		{[][3]int64{{0, 5, 4}}, 5, 9, 0},             // 恰在 5 相接不重叠
		{[][3]int64{{0, 5, 4}, {5, 9, 7}}, 2, 7, 7},  // 跨两条取峰值
		{[][3]int64{{0, 9, 1}, {2, 4, 9}}, 2, 4, 10}, // 嵌套相加
		{[][3]int64{{0, 9, 1}, {2, 4, 9}}, 4, 5, 1},  // 右开：4 处只剩前者
		{[][3]int64{{0, 5, 4}, {5, 9, 7}, {0, 9, 1}}, 0, 9, 8},
	}
	for i, tc := range cases {
		c := NewCore(100)
		for _, iv := range tc.ivs {
			c.Add(iv[0], iv[1], iv[2])
		}
		if got := c.Peak(tc.s, tc.e); got != tc.wantPeak {
			t.Fatalf("case %d: Peak=%d want %d", i, got, tc.wantPeak)
		}
	}
}

// 随机序列下 Peak/Add/Remove 与朴素参照一致。
func TestPeakRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	c := NewCore(1 << 40)
	var ivs [][3]int64
	for step := 0; step < 500; step++ {
		if len(ivs) > 0 && rng.Intn(3) == 0 {
			i := rng.Intn(len(ivs))
			if !c.Remove(ivs[i][0], ivs[i][1], ivs[i][2]) {
				t.Fatalf("remove %v failed", ivs[i])
			}
			ivs = append(ivs[:i], ivs[i+1:]...)
			continue
		}
		a, b := rng.Int63n(40), rng.Int63n(40)
		if a > b {
			a, b = b, a
		}
		b++
		n := 1 + rng.Int63n(9)
		c.Add(a, b, n)
		ivs = append(ivs, [3]int64{a, b, n})
		qs, qe := rng.Int63n(40), rng.Int63n(40)
		if qs > qe {
			qs, qe = qe, qs
		}
		qe++
		var want int64 // 朴素：逐点求覆盖和的最大值
		for x := qs; x < qe; x++ {
			var sum int64
			for _, iv := range ivs {
				if iv[0] <= x && x < iv[1] {
					sum += iv[2]
				}
			}
			if sum > want {
				want = sum
			}
		}
		if got := c.Peak(qs, qe); got != want {
			t.Fatalf("step %d: Peak(%d,%d)=%d want %d", step, qs, qe, got, want)
		}
	}
}
