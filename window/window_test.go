package window

import "testing"

// TestObserveExample 复刻规格中 N=2,M=3 的 8 条序列：
// cnt=1、2 放行；3、4 丢弃；5 放行；6、7 丢弃；8 放行；dropped=4。
func TestObserveExample(t *testing.T) {
	p := Params{N: 2, M: 3}
	e := Entry{Win: 0}
	wantKept := []bool{true, true, false, false, true, false, false, true}
	for i, want := range wantKept {
		got := e.Observe(p)
		t.Logf("输入=第%d条 sev<4 | cnt=%d | 判定 kept=%v（依据 cnt<=N 或 (cnt-N)%%M==0）", i+1, e.Cnt, got)
		if got != want {
			t.Fatalf("第%d条 kept=%v want %v; dropped=%d", i+1, got, want, e.Dropped)
		}
	}
	if e.Dropped != 4 {
		t.Fatalf("dropped=%d want 4", e.Dropped)
	}
}

// TestNZero 覆盖 N=0：从第 1 条起每 M 条放一条。
func TestNZero(t *testing.T) {
	p := Params{N: 0, M: 3}
	e := Entry{Win: 0}
	wantKept := []bool{false, false, true, false, false, true}
	for i, want := range wantKept {
		got := e.Observe(p)
		t.Logf("N=0 输入=第%d条 | cnt=%d | kept=%v dropped=%d", i+1, e.Cnt, got, e.Dropped)
		if got != want {
			t.Fatalf("N=0 第%d条 kept=%v want %v", i+1, got, want)
		}
	}
}

// TestMOne 覆盖 M=1：前 N 条之后条条放行，永不丢弃。
func TestMOne(t *testing.T) {
	p := Params{N: 2, M: 1}
	e := Entry{Win: 0}
	for i := 1; i <= 10; i++ {
		got := e.Observe(p)
		t.Logf("M=1 输入=第%d条 | cnt=%d | kept=%v", i, e.Cnt, got)
		if !got {
			t.Fatalf("M=1 第%d条不应丢弃, dropped=%d", i, e.Dropped)
		}
	}
	if e.Dropped != 0 {
		t.Fatalf("M=1 dropped=%d want 0", e.Dropped)
	}
}

// TestBoundaryOffByOne 覆盖 (cnt−N) 恰为 M 的倍数（放行）与差 1（丢弃）。
func TestBoundaryOffByOne(t *testing.T) {
	p := Params{N: 3, M: 5}
	e := Entry{Win: 0}
	// cnt=4(差1→丢),5(倍数? (5-3)=2 丢),6,7 丢；cnt=8 → (8-3)=5 倍数放行；
	// cnt=9 → 差到下一个倍数为 1，丢弃。
	want := map[int]bool{1: true, 2: true, 3: true, 4: false, 5: false, 6: false, 7: false, 8: true, 9: false}
	for cnt := 1; cnt <= 9; cnt++ {
		got := e.Observe(p)
		t.Logf("边界 输入 cnt=%d (cnt-N)=%d | kept=%v（%d%%5==0 为放行）",
			cnt, cnt-3, got, cnt-3)
		if got != want[cnt] {
			t.Fatalf("cnt=%d kept=%v want %v", cnt, got, want[cnt])
		}
	}
}

// TestRollOver 验证窗口切换重置计数并交付旧窗口 dropped；同窗口不交付。
func TestRollOver(t *testing.T) {
	p := Params{N: 1, M: 10}
	e := Entry{Win: 0}
	e.Observe(p) // cnt=1 放行
	e.Observe(p) // cnt=2 丢弃 → dropped=1
	if _, d, ok := e.RollOver(0); ok || d != 0 {
		t.Fatalf("同窗口不应重置: d=%d ok=%v", d, ok)
	}
	oldWin, d, ok := e.RollOver(1)
	t.Logf("输入 RollOver cur=1 | 输出 dropped=%d ok=%v（旧 win=0 的未交付计数）", d, ok)
	if !ok || oldWin != 0 || d != 1 || e.Win != 1 || e.Cnt != 0 || e.Dropped != 0 {
		t.Fatalf("窗口切换结果异常: %+v d=%d ok=%v", e, d, ok)
	}
	// 新窗口零丢弃时滚动只重置，不交付。
	if _, d, ok := e.RollOver(2); ok || d != 0 {
		t.Fatalf("无丢弃时不应交付: d=%d ok=%v", d, ok)
	}
}
