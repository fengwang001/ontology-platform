package mon

import (
	"math/rand"
	"testing"

	"ontology/sla"
)

// bruteWindow 暴力维护最近 w 个完成的违例标记，用于与环形缓冲逐项对拍。
func bruteWindow(hist []bool, w int) (viol int) {
	lo := len(hist) - w
	if lo < 0 {
		lo = 0
	}
	for _, v := range hist[lo:] {
		if v {
			viol++
		}
	}
	return viol
}

// TestRingWindowAndCheckCount 钉住不变量3（窗口精确）与复杂度（checks 为与 m
// 无关的小常数：未满=1、已满=2），多档 m 用循环生成随机延迟对拍。
func TestRingWindowAndCheckCount(t *testing.T) {
	const w, k = 4, 3
	cases := []int{100, 500, 1000, 5000, 10000}
	for _, mWant := range cases {
		m, err := New(10, w, k)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		rng := rand.New(rand.NewSource(int64(mWant)))
		hist := make([]bool, 0, mWant+1)
		for i := 0; i < mWant; i++ {
			lat := int64(rng.Intn(25)) // 0..24，跨越阈值 10 两侧
			out := m.Complete(lat)
			hist = append(hist, out == sla.Violation)
			if m.Breached() != (bruteWindow(hist, w) >= k) {
				t.Fatalf("m=%d i=%d Breached 与暴力重算不符", mWant, i)
			}
		}
		// 再完成一个，断言本次为更新窗口检查的记录数不随 m 增长。
		out := m.Complete(12)
		hist = append(hist, out == sla.Violation)
		if m.checks > 2 {
			t.Fatalf("m=%d checks=%d，随规模线性增长，非 O(1)", mWant, m.checks)
		}
		if m.filled != w {
			t.Fatalf("m=%d 窗口应已满 filled=%d", mWant, m.filled)
		}
		if m.checks != 2 { // 满窗：检查被滑出的 1 条 + 滑入的 1 条
			t.Fatalf("m=%d 满窗 checks=%d 期望 2", mWant, m.checks)
		}
		if m.windowViol != int64(bruteWindow(hist, w)) {
			t.Fatalf("m=%d windowViol=%d 暴力=%d", mWant, m.windowViol, bruteWindow(hist, w))
		}
	}
}

// TestRingWindowPartialFill 钉住不足 W 个完成时取全部，且未满 checks==1。
func TestRingWindowPartialFill(t *testing.T) {
	m, _ := New(10, 4, 3)
	seq := []int64{15, 5, 16} // 违、OK、违
	var viol int
	for i, lat := range seq {
		m.Begin()
		out := m.Complete(lat)
		if out == sla.Violation {
			viol++
		}
		if m.checks != 1 {
			t.Fatalf("i=%d 未满窗 checks=%d 期望 1", i, m.checks)
		}
		if m.Breached() != (viol >= 3) {
			t.Fatalf("i=%d 部分窗口 Breached 错误", i)
		}
	}
	if m.Count() != 3 || m.InFlight() != 0 {
		t.Fatalf("Complete 计数异常 count=%d inFlight=%d", m.Count(), m.InFlight())
	}
}

// TestRingWindowSlipOut 钉住最旧违例滑出后运行违例计数随之减少（告警非粘滞）。
func TestRingWindowSlipOut(t *testing.T) {
	m, _ := New(10, 4, 3)
	for _, lat := range []int64{11, 11, 11, 4} { // 违违违OK -> breach
		m.Complete(lat)
	}
	if !m.Breached() {
		t.Fatal("近4违例=3 应 breach")
	}
	m.Complete(4) // 滑出最旧违例：近4=违违OKOK -> 清除
	if m.Breached() {
		t.Fatal("最旧违例滑出后应清除，告警不得粘滞")
	}
	if m.windowViol != 2 {
		t.Fatalf("windowViol=%d 期望 2", m.windowViol)
	}
}
