package cfs

import "testing"

// TestReplenishNeedMakesLOne 补给 need=1-l，解除节流后 l 恰为 1。
func TestReplenishNeedMakesLOne(t *testing.T) {
	c := mustNew(t, 10, 100, 3, 0, 1)
	mustOp(t, c.Wake(0, 0), "Wake")
	// l=-9，want=12，take=10，l=-9+10=1>0？不：take=min(12,10)=10，l=1，运行。
	// 为构造节流，用更大消耗：l=0-12=-12，want=15，take=10，l=-2，节流。
	mustOp(t, c.Run(1, 0, 12), "Run(1,0,12)")
	mustState(t, c, 0, Throttled, -2, 1)
	// 边界 100：G=10，need=1-(-2)=3，take=3，l 恰为 1 解除节流
	mustOp(t, c.Run(100, 0, 1), "Run(100,0,1)")
	// 解除后 l=1，再 Run：l=0，want=3，take=3，l=3
	mustState(t, c, 0, Running, 3, 1)
	mustStats(t, c, 1, 1, 99)
}

// TestPartialReplenishStarvesRest G 不足时部分补给仍节流，后面的 CPU 完全得不到。
func TestPartialReplenishStarvesRest(t *testing.T) {
	c := mustNew(t, 3, 10, 2, 0, 3)
	for i := 0; i < 3; i++ {
		mustOp(t, c.Wake(0, i), "Wake")
	}
	// cpu0：l=-4，want=6，take=3，l=-1，节流，G=0
	mustOp(t, c.Run(1, 0, 4), "Run(1,0,4)")
	// cpu1、cpu2：take=0，l=-4，节流
	mustOp(t, c.Run(1, 1, 4), "Run(1,1,4)")
	mustOp(t, c.Run(1, 2, 4), "Run(1,2,4)")
	mustQueue(t, c, 0, 1, 2)

	// 边界 10：G=3；cpu0 need=2 take=2 l=1 解除，G=1；
	// cpu1 need=5 take=1 l=-3 仍节流，G=0；cpu2 完全得不到，l=-4
	mustOp(t, c.Idle(10, 0), "Idle(10,0)")
	mustState(t, c, 1, Throttled, -3, 1)
	mustState(t, c, 2, Throttled, -4, 1)
	mustQueue(t, c, 1, 2)
	mustPool(t, c, 0)
}

// TestReplenishOrderFIFO 补给次序为节流先后而非编号：cpu1 先于 cpu0 节流。
func TestReplenishOrderFIFO(t *testing.T) {
	c := mustNew(t, 4, 10, 2, 0, 2)
	mustOp(t, c.Wake(0, 0), "Wake(0,0)")
	mustOp(t, c.Wake(0, 1), "Wake(0,1)")
	// cpu1 先节流：l=-6，want=8，take=4，l=-2，G=0，since=1
	mustOp(t, c.Run(1, 1, 6), "Run(1,1,6)")
	// cpu0 后节流：take=0，l=-6，since=2
	mustOp(t, c.Run(2, 0, 6), "Run(2,0,6)")
	mustQueue(t, c, 1, 0)

	// 边界 10：G=4；先补给 cpu1：need=3，take=3，l=1 解除，G=1；
	// 再补给 cpu0：need=7，take=1，l=-5 仍节流
	mustOp(t, c.Idle(10, 1), "Idle(10,1)")
	mustState(t, c, 0, Throttled, -5, 2)
	mustState(t, c, 1, Idle, 1, 1)
	mustQueue(t, c, 0)
	mustStats(t, c, 1, 2, 9) // throttledTime = 10-1 = 9（仅 cpu1 解除）
}

// TestIdleReturnThreshold Idle 时 l 恰为 1 不归还，恰为 2 归还 1。
func TestIdleReturnThreshold(t *testing.T) {
	c := mustNew(t, 20, 100, 5, 0, 2)
	mustOp(t, c.Wake(0, 0), "Wake(0,0)")
	mustOp(t, c.Wake(0, 1), "Wake(0,1)")
	// cpu0：l=-4，want=9，take=9，l=5，G=11
	mustOp(t, c.Run(1, 0, 4), "Run(1,0,4)")
	// cpu1：l=-4，want=9，take=9，l=5，G=2
	mustOp(t, c.Run(1, 1, 4), "Run(1,1,4)")
	// cpu0 跑到 l=1：l=5-4=1>0 不借
	mustOp(t, c.Run(2, 0, 4), "Run(2,0,4)")
	// cpu1 跑到 l=2：l=5-3=2>0 不借
	mustOp(t, c.Run(2, 1, 3), "Run(2,1,3)")
	mustPool(t, c, 2)

	// l 恰为 1：不归还
	mustOp(t, c.Idle(3, 0), "Idle(3,0)")
	mustState(t, c, 0, Idle, 1, 0)
	mustPool(t, c, 2)

	// l 恰为 2：归还 slack=1，l=1
	mustOp(t, c.Idle(3, 1), "Idle(3,1)")
	mustState(t, c, 1, Idle, 1, 0)
	mustPool(t, c, 3)
}

// TestIdleReturnCapped 归还受 Q+B 封顶。
func TestIdleReturnCapped(t *testing.T) {
	c := mustNew(t, 10, 100, 8, 5, 1) // Q+B=15
	mustOp(t, c.Wake(0, 0), "Wake")
	// l=-2，want=10，take=10，l=8，G=0
	mustOp(t, c.Run(1, 0, 2), "Run(1,0,2)")
	mustPool(t, c, 0)
	// 边界 100：G=min(15, 0+10)=10；再 Run：l=7
	mustOp(t, c.Run(100, 0, 1), "Run(100,0,1)")
	mustPool(t, c, 10)
	// Idle：l=7>1，slack=6，G=min(15, 10+6)=15（封顶），l=1
	mustOp(t, c.Idle(101, 0), "Idle(101,0)")
	mustState(t, c, 0, Idle, 1, 0)
	mustPool(t, c, 15)
}

// TestBoundaryReplenishCapped 边界补满受 Q+B 封顶。
func TestBoundaryReplenishCapped(t *testing.T) {
	c := mustNew(t, 10, 100, 5, 5, 1) // Q+B=15
	mustOp(t, c.Wake(0, 0), "Wake")
	// l=-2，want=7，take=7，l=5，G=3
	mustOp(t, c.Run(1, 0, 2), "Run(1,0,2)")
	// 边界 100：G=min(15, 3+10)=13；边界 200：G=min(15, 23)=15（封顶）
	mustOp(t, c.Run(200, 0, 1), "Run(200,0,1)")
	mustPool(t, c, 15)
	mustStats(t, c, 2, 0, 0)
}
