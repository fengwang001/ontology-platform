// Command demo 逐条演示并判定剖析器的核心性质，全部 OK 时退出码为 0。
package main

import (
	"fmt"
	"os"
)

var (
	checks int
	fails  int
)

func report(ok bool, format string, args ...any) {
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
		fails++
	}
	checks++
	fmt.Printf("%s %s\n", verdict, fmt.Sprintf(format, args...))
}

func main() {
	checkSelfTotalIdentity()
	checkTruncation()
	checkRecursionAttribution()
	checkDropped()
	checkClockRollback()
	checkDumpTruncation()
	checkRecoveredTree()
	checkInsertCost()
	fmt.Printf("TOTAL %d checks, %d fail\n", checks, fails)
	if checks == 0 || fails != 0 {
		os.Exit(1)
	}
}
