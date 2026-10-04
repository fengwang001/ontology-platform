package phase

import "testing"

func TestPhaseLifecycle(t *testing.T) {
	// T=60, X=1, Hmax=3, lastCall=9700。
	p := New(60, 1, 3, 9700)
	if p.Halted() || p.HaltCount() != 0 {
		t.Fatalf("initial phase should be continuous")
	}
	he := p.Begin(11)
	if he != 71 || !p.Halted() || p.HaltCount() != 1 || p.End() != 71 {
		t.Fatalf("begin: he=%d halted=%v halts=%d", he, p.Halted(), p.HaltCount())
	}
	// 恢复价 1081 相对 Rs=1000、De=800bp 超带；可延长一次。
	if !p.CanExtend(1000, 1081, 800) {
		t.Fatalf("1081 vs 1000 over 800bp should be extendable")
	}
	if p.CanExtend(1000, 1080, 800) {
		t.Fatalf("1080 vs 1000 at band equality must not extend")
	}
	if got := p.Extend(); got != 131 || p.Extensions() != 1 {
		t.Fatalf("extend he=%d ext=%d, want 131/1", got, p.Extensions())
	}
	// 延长次数已达 X：不再延长。
	if p.CanExtend(1000, 1081, 800) {
		t.Fatalf("extension budget exhausted")
	}
	p.Recover()
	if p.Halted() || p.Extensions() != 0 {
		t.Fatalf("recover should reset halted and per-halt extensions")
	}
	// 中断次数跨多次中断累计，直到 Hmax（计数由 gate 判定，此处验证计数）。
	p.Begin(1000)
	p.Recover()
	p.Begin(2000)
	p.Recover()
	if p.HaltCount() != 3 {
		t.Fatalf("halt count = %d, want 3", p.HaltCount())
	}
}

func TestPhaseEndClampedToLastCall(t *testing.T) {
	// now=9660, T=60 -> 9720，但尾盘起点 9700：he 截断到 9700。
	p := New(60, 2, 5, 9700)
	if he := p.Begin(9660); he != 9700 {
		t.Fatalf("he=%d, want 9700", he)
	}
	// he 已到尾盘起点：即使恢复价超带也不得延长。
	if p.CanExtend(1000, 1100, 1) {
		t.Fatalf("must not extend once he reached lastCall")
	}
	// 尾盘之前触发，一次延长恰好到 lastCall：第二次不得再延。
	q := New(40, 2, 5, 9700)
	q.Begin(9630) // he=9670
	if he := q.Extend(); he != 9700 || q.Extensions() != 1 {
		t.Fatalf("he=%d ext=%d, want 9700/1", he, q.Extensions())
	}
	if q.CanExtend(1000, 1100, 1) {
		t.Fatalf("he at lastCall must forbid further extension")
	}
}

func TestPhaseXZeroNeverExtends(t *testing.T) {
	p := New(60, 0, 3, 9700)
	p.Begin(10)
	if p.CanExtend(1000, 1100, 1) {
		t.Fatalf("X=0 must never extend even wildly over band")
	}
}
