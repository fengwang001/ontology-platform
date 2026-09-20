// demo 逐条自检 fsm 包的 8 项语义，打印 OK/FAIL。
// 无论结果如何都以退出码 0 结束。
package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	results := []check{
		{"1 construction validation", checkConstruction()},
		{"2 illegal event no side effects", checkIllegal()},
		{"3 self transition skips actions", checkSelf()},
		{"4 entry failure stays put", checkEntryFail()},
		{"5 terminal absorbs events", checkTerminal()},
		{"6 actions once and ordered", checkOrdering()},
		{"7 log matches observations", checkLogObserve()},
		{"8 concurrency safe", checkConcurrency()},
	}

	fails := 0
	for _, r := range results {
		status := "OK"
		if !r.ok {
			status = "FAIL"
			fails++
		}
		fmt.Printf("[%s] %s\n", status, r.name)
	}
	fmt.Printf("summary: %d/%d passed\n", len(results)-fails, len(results))
}
