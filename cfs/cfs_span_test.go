package cfs

import "testing"

// TestRejectedOpDoesNotCommit 被拒绝的操作不得改变任何状态，
// 包括本应在该操作之前处理的边界（先在副本上推演）。
func TestRejectedOpDoesNotCommit(t *testing.T) {
	c := mustNew(t, 10, 100, 5, 0, 2)
	mustOp(t, c.Wake(0, 0), "Wake(0,0)")
	mustOp(t, c.Run(1, 0, 2), "Run(1,0,2)") // l=5, G=3
	snapshot := func() (int64, int64, int64, int64, int64) {
		p, n, tt := c.Stats()
		_, l0, _ := c.State(0)
		_, l1, _ := c.State(1)
		return p, n, tt, l0, l1
	}
	b0, b1, b2, b3, b4 := snapshot()
	gBefore := c.Pool()

	// now=500 有 5 个边界待处理；cpu1 空闲，Run 必被拒
	mustReject(t, c.Run(500, 1, 1), ErrNotRunning, "Run(500, 空闲)")
	// 边界未被提前提交
	a0, a1, a2, a3, a4 := snapshot()
	if a0 != b0 || a1 != b1 || a2 != b2 || a3 != b3 || a4 != b4 {
		t.Fatalf("拒绝后统计或 l 发生变化")
	}
	if c.Pool() != gBefore {
		t.Fatalf("拒绝后 G 发生变化: %d -> %d", gBefore, c.Pool())
	}
	if iters := c.st.boundaryIters; iters != 0 {
		t.Fatalf("拒绝后 boundaryIters = %d, 期望 0", iters)
	}
	// 后续合法操作正常处理这 5 个边界：G=min(10, 3+50)=10，periods=5
	mustOp(t, c.Run(500, 0, 1), "Run(500,0,1)")
	mustStats(t, c, 5, 0, 0)
	mustPool(t, c, 10)
}

// TestLargeSpanNoOverflow 大跨度时 G+k*Q 不溢出（k 可达 10^15）。
func TestLargeSpanNoOverflow(t *testing.T) {
	c := mustNew(t, 1_000_000_000, 1, 1, 1_000_000_000, 1) // Q+B=2e9
	mustOp(t, c.Wake(0, 0), "Wake")
	// now=1e15：k=1e15 个边界，k*Q=1e24 远超 int64，须防溢出；
	// G=min(2e9, 1e9+1e24)=2e9，periods=1e15
	mustOp(t, c.Idle(1_000_000_000_000_000, 0), "Idle(1e15)")
	mustPool(t, c, 2_000_000_000)
	p, _, _ := c.Stats()
	if p != 1_000_000_000_000_000 {
		t.Fatalf("periods = %d, 期望 1e15", p)
	}
	if iters := c.st.boundaryIters; iters != 0 {
		t.Fatalf("boundaryIters = %d, 期望 0", iters)
	}
}

// TestBoundaryItersCounter P=1、Q=1、B=0，欠 1000 单位后 Run 在 now=1e15
// 必须很快完成，boundaryIters 不超过 1010。
func TestBoundaryItersCounter(t *testing.T) {
	c := mustNew(t, 1, 1, 1, 0, 1)
	mustOp(t, c.Wake(0, 0), "Wake")
	// l=0-1000=-1000，want=1001，take=min(1001,1)=1，l=-999<=0，节流 since=0，G=0
	mustOp(t, c.Run(0, 0, 1000), "Run(0,0,1000)")
	mustState(t, c, 0, Throttled, -999, 0)

	// now=1e15：每个边界补 1，l 从 -999 起需 1000 个边界到 l=1 解除（b=1000），
	// 之后队列空，剩余约 1e15 个边界算术一次完成
	mustOp(t, c.Run(1_000_000_000_000_000, 0, 1), "Run(1e15)")
	if iters := c.st.boundaryIters; iters > 1010 {
		t.Fatalf("boundaryIters = %d, 超过 1010", iters)
	}
	mustState(t, c, 0, Running, 1, 0)
	p, _, tt := c.Stats()
	if p != 1_000_000_000_000_000 {
		t.Fatalf("periods = %d, 期望 1e15", p)
	}
	if tt != 1000 {
		t.Fatalf("throttledTime = %d, 期望 1000（b=1000 解除，since=0）", tt)
	}
	// 解除后 l=1，Run 消耗 1 得 l=0，再借 want=1、take=1，l=1，G=0
	mustPool(t, c, 0)
}
