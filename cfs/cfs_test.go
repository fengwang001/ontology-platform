package cfs

import (
	"reflect"
	"testing"
)

func mustNew(t *testing.T, q, p, s, b int64, cpus int) *Controller {
	t.Helper()
	c, err := New(q, p, s, b, cpus)
	if err != nil {
		t.Fatalf("New(%d,%d,%d,%d,%d) 被拒: %v", q, p, s, b, cpus, err)
	}
	return c
}

func mustOp(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s 应被接受, 实际被拒: %v", what, err)
	}
}

func mustState(t *testing.T, c *Controller, cpu int, st CPUState, l, since int64) {
	t.Helper()
	gotSt, gotL, gotSince := c.State(cpu)
	if gotSt != st || gotL != l || gotSince != since {
		t.Fatalf("State(%d) = (%v,%d,%d), 期望 (%v,%d,%d)",
			cpu, gotSt, gotL, gotSince, st, l, since)
	}
}

func mustPool(t *testing.T, c *Controller, g int64) {
	t.Helper()
	if got := c.Pool(); got != g {
		t.Fatalf("Pool() = %d, 期望 %d", got, g)
	}
}

func mustStats(t *testing.T, c *Controller, periods, nThrottled, throttledTime int64) {
	t.Helper()
	p, n, tt := c.Stats()
	if p != periods || n != nThrottled || tt != throttledTime {
		t.Fatalf("Stats() = (%d,%d,%d), 期望 (%d,%d,%d)",
			p, n, tt, periods, nThrottled, throttledTime)
	}
}

func mustQueue(t *testing.T, c *Controller, want ...int) {
	t.Helper()
	got := c.Queue()
	if len(want) == 0 && len(got) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Queue() = %v, 期望 %v", got, want)
	}
}

// TestSpecExampleBurstZero 逐条复现题目示例（B=0）：Q=20、P=100、S=5、C=2。
func TestSpecExampleBurstZero(t *testing.T) {
	c := mustNew(t, 20, 100, 5, 0, 2)
	mustPool(t, c, 20)

	mustOp(t, c.Wake(0, 0), "Wake(0,0)")
	mustOp(t, c.Wake(0, 1), "Wake(0,1)")

	// l0=-3, want=5-(-3)=8, take=8, l0=5, G=12（l 为负时多借，want=S-l）
	mustOp(t, c.Run(1, 0, 3), "Run(1,0,3)")
	mustState(t, c, 0, Running, 5, 0)
	mustPool(t, c, 12)

	// l1=-10, want=15, take=min(15,12)=12, l1=2, G=0，仍为运行
	mustOp(t, c.Run(2, 1, 10), "Run(2,1,10)")
	mustState(t, c, 1, Running, 2, 0)
	mustPool(t, c, 0)

	// l0=5-6=-1, want=6, take=0, l0=-1，节流 since=3
	mustOp(t, c.Run(3, 0, 6), "Run(3,0,6)")
	mustState(t, c, 0, Throttled, -1, 3)
	mustQueue(t, c, 0)

	// l1=2-5=-3, take=0，节流 since=4，nThrottled=2
	mustOp(t, c.Run(4, 1, 5), "Run(4,1,5)")
	mustState(t, c, 1, Throttled, -3, 4)
	mustQueue(t, c, 0, 1)
	mustStats(t, c, 0, 2, 0)

	// 先处理边界 100：G=20；cpu0 need=2 take=2 l0=1 解除，throttledTime+=97，G=18；
	// cpu1 need=4 take=4 l1=1 解除，throttledTime=97+96=193，G=14。
	// 再执行 Run：l0=1-1=0，want=2... 实际 want=S-l=5，take=5，l0=5，G=9。
	mustOp(t, c.Run(150, 0, 1), "Run(150,0,1)")
	mustState(t, c, 0, Running, 5, 3)
	mustState(t, c, 1, Running, 1, 4)
	mustPool(t, c, 9)
	mustQueue(t, c)
	mustStats(t, c, 1, 2, 193)

	// l0=5>1，slack=4，G=min(20,9+4)=13，l0=1，空闲
	mustOp(t, c.Idle(170, 0), "Idle(170,0)")
	mustState(t, c, 0, Idle, 1, 3)
	mustPool(t, c, 13)
	mustStats(t, c, 1, 2, 193)
}
