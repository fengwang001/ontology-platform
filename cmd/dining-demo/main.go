// dining-demo 在两个进程的冲突边上演示脏/净叉协议的完整一轮，
// 并把每次操作的输入、输出与判定依据打印到标准输出。
package main

import (
	"fmt"

	"ontology/dining"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func deliverAll(c *dining.Coordinator) {
	for {
		pending := c.Pending()
		if len(pending) == 0 {
			return
		}
		must(c.Deliver(pending[0]))
	}
}

func report(c *dining.Coordinator, tag string) {
	if v := c.CheckInvariants(); len(v) != 0 {
		panic(fmt.Sprintf("invariant violated at %s: %v", tag, v))
	}
	fmt.Printf("# %s: invariants OK\n", tag)
}

func main() {
	net := dining.NewQueueNetwork()
	log := dining.NewStdLogger(nil)
	c, err := dining.NewCoordinator([]int{0, 1}, [][2]int{{0, 1}}, net, log)
	must(err)

	must(c.BecomeHungry(1))
	deliverAll(c)
	must(c.StartEating(1))
	report(c, "p1 eating")

	// p0 在 p1 进餐期间饥饿：请求到达后应被暂存。
	must(c.BecomeHungry(0))
	deliverAll(c)
	report(c, "p0 hungry while p1 eats")

	must(c.FinishEating(1))
	deliverAll(c)
	must(c.StartEating(0))
	report(c, "p0 eating after deferred grant")

	// 演示一次被拒绝的非法操作（思考中/缺叉进餐不可能同时发生；这里尝试
	// 让不存在的进程进餐）。
	if err := c.StartEating(9); err == nil {
		panic("expected rejection")
	} else {
		fmt.Printf("# rejected as expected: %v\n", err)
	}
	must(c.FinishEating(0))
	fmt.Println("# demo finished: mutual exclusion, acyclic precedence hold")
}
