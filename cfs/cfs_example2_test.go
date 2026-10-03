package cfs

import "testing"

// TestSpecExampleBurstCarry 逐条复现题目示例（B=15）：Q=10、P=100、S=5、C=1。
func TestSpecExampleBurstCarry(t *testing.T) {
	c := mustNew(t, 10, 100, 5, 15, 1)
	mustPool(t, c, 10)

	mustOp(t, c.Wake(0, 0), "Wake(0,0)")

	// l=-2, want=7, take=7, l=5, G=3
	mustOp(t, c.Run(1, 0, 2), "Run(1,0,2)")
	mustState(t, c, 0, Running, 5, 0)
	mustPool(t, c, 3)

	// 边界 100：G=min(25, 3+10)=13（余量 3 结转），periods=1；再 Run：l=4
	mustOp(t, c.Run(100, 0, 1), "Run(100,0,1)")
	mustPool(t, c, 13)
	mustState(t, c, 0, Running, 4, 0)
	mustStats(t, c, 1, 0, 0)

	// 边界 200：G=min(25, 13+10)=23；l=3
	mustOp(t, c.Run(200, 0, 1), "Run(200,0,1)")
	mustPool(t, c, 23)
	mustState(t, c, 0, Running, 3, 0)

	// 边界 300：G=min(25, 33)=25（被 Q+B 封顶）；l=2
	mustOp(t, c.Run(300, 0, 1), "Run(300,0,1)")
	mustPool(t, c, 25)
	mustState(t, c, 0, Running, 2, 0)
	mustStats(t, c, 3, 0, 0)

	// 边界 400..1000 共 7 个，无节流者，算术一次完成：
	// G=min(25, 25+70)=25，periods=10；l=1
	mustOp(t, c.Run(1000, 0, 1), "Run(1000,0,1)")
	mustPool(t, c, 25)
	mustState(t, c, 0, Running, 1, 0)
	mustStats(t, c, 10, 0, 0)
	if iters := c.st.boundaryIters; iters != 0 {
		t.Fatalf("boundaryIters = %d, 期望 0（队列空，应算术一次完成）", iters)
	}
}

// TestBurstZeroResetsPool B=0 时每边界等价重置为 Q（对照 B>0 的结转）。
func TestBurstZeroResetsPool(t *testing.T) {
	c := mustNew(t, 10, 100, 5, 0, 1)
	mustOp(t, c.Wake(0, 0), "Wake(0,0)")
	mustOp(t, c.Run(1, 0, 2), "Run(1,0,2)") // l=5, G=3
	mustPool(t, c, 3)
	// B=0：边界 100 后 G=min(10, 3+10)=10，而不是结转后的 13
	mustOp(t, c.Run(100, 0, 1), "Run(100,0,1)")
	mustPool(t, c, 10)
	mustState(t, c, 0, Running, 4, 0)
}
