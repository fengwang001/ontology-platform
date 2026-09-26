package est

import "testing"

// TestEstimateReadsExactlyM 钉住复杂度约束：无论注入多少元素，
// Estimate 为计算 mean 访问的寄存器个数恒等于 m（只读 m 个寄存器，
// 而不是记录全部 N 个元素再精确去重）。同包白盒读取非导出计数器。
func TestEstimateReadsExactlyM(t *testing.T) {
	for _, m := range []int{2, 8, 64, 1024} {
		for _, n := range []int{100, 500, 1000, 5000, 10000} {
			e := New(m)
			for i := 0; i < n; i++ {
				e.Add((i*31+7)%m, (i*17+3)%50)
			}
			_ = e.Estimate()
			if e.reads != m {
				t.Errorf("m=%d N=%d: Estimate 访问了 %d 个寄存器，应为 %d", m, n, e.reads, m)
			}
			if !e.LastEstimateReadAll() {
				t.Errorf("m=%d N=%d: LastEstimateReadAll() = false", m, n)
			}
		}
	}
}

// TestAlphaTable 钉住 α_m 查表与公式一致、取值收敛到 LogLog 常数 0.39701。
func TestAlphaTable(t *testing.T) {
	prev := 0.0
	for m := 2; m <= 1<<16; m <<= 1 {
		a := Alpha(m)
		if a != alphaFormula(m) {
			t.Errorf("m=%d: 查表值 %v 与公式值 %v 不一致", m, a, alphaFormula(m))
		}
		if a <= prev {
			t.Errorf("m=%d: α_m=%v 应单调上升收敛", m, a)
		}
		prev = a
	}
	if a := Alpha(1 << 20); a < 0.396 || a > 0.398 {
		t.Errorf("α_(2^20)=%v 应约等于 0.39701", a)
	}
	if Alpha(1) != 0.5 {
		t.Errorf("α_1 应为退化常数 0.5，得到 %v", Alpha(1))
	}
}
