package cfs

import "testing"

// TestBorrowWantIsSMinusL 借用量为 S-l：l 为负时借得更多。
func TestBorrowWantIsSMinusL(t *testing.T) {
	c := mustNew(t, 100, 1000, 5, 0, 1)
	mustOp(t, c.Wake(0, 0), "Wake")
	// l=0-8=-8，want=5-(-8)=13，take=13，l=5，G=87
	mustOp(t, c.Run(1, 0, 8), "Run(1,0,8)")
	mustState(t, c, 0, Running, 5, 0)
	mustPool(t, c, 87)
}

// TestExactBorrowNoThrottle G 恰好够借时不节流。
func TestExactBorrowNoThrottle(t *testing.T) {
	c := mustNew(t, 10, 100, 5, 0, 1)
	mustOp(t, c.Wake(0, 0), "Wake")
	// l=-5，want=10，G=10 恰好够：take=10，l=5，G=0，仍运行
	mustOp(t, c.Run(1, 0, 5), "Run(1,0,5)")
	mustState(t, c, 0, Running, 5, 0)
	mustPool(t, c, 0)
	mustStats(t, c, 0, 0, 0)
}

// TestBorrowShortByOneThrottles G 差 1 不够借时节流。
func TestBorrowShortByOneThrottles(t *testing.T) {
	c := mustNew(t, 4, 100, 5, 0, 1)
	mustOp(t, c.Wake(0, 0), "Wake")
	// l=-5，want=10，G=4：take=4，l=-1<=0，节流
	mustOp(t, c.Run(1, 0, 5), "Run(1,0,5)")
	mustState(t, c, 0, Throttled, -1, 1)
	mustPool(t, c, 0)
	mustStats(t, c, 0, 1, 0)
}

// TestZeroAfterBorrowThrottles 借到后 l 恰为 0 仍节流（l 不大于 0 即节流）。
func TestZeroAfterBorrowThrottles(t *testing.T) {
	c := mustNew(t, 5, 100, 5, 0, 1)
	mustOp(t, c.Wake(0, 0), "Wake")
	// l=-5，want=10，take=5，l=0：恰为 0 即节流
	mustOp(t, c.Run(1, 0, 5), "Run(1,0,5)")
	mustState(t, c, 0, Throttled, 0, 1)
	mustPool(t, c, 0)
	mustStats(t, c, 0, 1, 0)
}

// TestBorrowLeavesPositiveNoThrottle 借到后 l>0 不节流（G 恰好借到 l=1）。
func TestBorrowLeavesPositiveNoThrottle(t *testing.T) {
	c := mustNew(t, 6, 100, 5, 0, 1)
	mustOp(t, c.Wake(0, 0), "Wake")
	// l=-5，want=10，take=6，l=1>0：不节流，G=0
	mustOp(t, c.Run(1, 0, 5), "Run(1,0,5)")
	mustState(t, c, 0, Running, 1, 0)
	mustPool(t, c, 0)
	mustStats(t, c, 0, 0, 0)
}

// TestBoundaryAtExactNowProcessedFirst 恰等于 now 的边界先于操作处理，
// 且被节流 CPU 在边界解除后同一时刻可运行。
func TestBoundaryAtExactNowProcessedFirst(t *testing.T) {
	c := mustNew(t, 5, 10, 2, 0, 1)
	mustOp(t, c.Wake(0, 0), "Wake")
	// l=-7，want=9，take=5，l=-2，节流 since=1，G=0
	mustOp(t, c.Run(1, 0, 7), "Run(1,0,7)")
	mustState(t, c, 0, Throttled, -2, 1)

	// now=10 恰为边界：先 G=5，need=3，take=3，l=1 解除节流，
	// throttledTime=10-1=9，G=2；再执行 Run：l=0，want=2，take=2，l=2，G=0
	mustOp(t, c.Run(10, 0, 1), "Run(10,0,1)")
	mustState(t, c, 0, Running, 2, 1)
	mustPool(t, c, 0)
	mustStats(t, c, 1, 1, 9)
}

// TestMultiBoundarySpanNoQueue 一次跨多个边界（队列空，纯算术）。
func TestMultiBoundarySpanNoQueue(t *testing.T) {
	c := mustNew(t, 3, 5, 2, 0, 1)
	mustOp(t, c.Wake(0, 0), "Wake")
	// now=26：边界 5..25 共 5 个，G=min(3, 3+15)=3，periods=5；
	// 再 Run：l=0-1=-1，want=3，take=3，l=2，G=0
	mustOp(t, c.Run(26, 0, 1), "Run(26,0,1)")
	mustPool(t, c, 0)
	mustStats(t, c, 5, 0, 0)
	mustState(t, c, 0, Running, 2, 0)
}

// TestMultiBoundarySpanWithQueue 跨多个边界且队列非空：逐个处理直到队列空。
func TestMultiBoundarySpanWithQueue(t *testing.T) {
	c := mustNew(t, 4, 10, 2, 0, 1)
	mustOp(t, c.Wake(0, 0), "Wake")
	// l=-6，want=8，take=4，l=-2，节流 since=1，G=0
	mustOp(t, c.Run(1, 0, 6), "Run(1,0,6)")
	mustState(t, c, 0, Throttled, -2, 1)

	// now=35：边界 10（G=4，need=3，take=3，l=1 解除，G=1，throttledTime=9），
	// 队列空后边界 20、30 算术完成：G=min(4, 1+8)=4，periods=3；
	// 再 Run：l=1-1=0，want=2，take=2，l=2，G=2
	mustOp(t, c.Run(35, 0, 1), "Run(35,0,1)")
	mustState(t, c, 0, Running, 2, 1)
	mustPool(t, c, 2)
	mustStats(t, c, 3, 1, 9)
	if iters := c.st.boundaryIters; iters != 1 {
		t.Fatalf("boundaryIters = %d, 期望 1（仅队列非空的边界逐个处理）", iters)
	}
}
