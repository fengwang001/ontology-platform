// demo 逐条演示多版本可见性存储的核心语义，每步一行 OK/FAIL。
package main

import (
	"fmt"
	"os"
)

type check struct {
	name string
	run  func() bool
}

func main() {
	checks := []check{
		{"快照隔离与可重复读", checkSnapshotIsolation},
		{"未提交不可见与读己之写", checkUncommitted},
		{"回滚无残留", checkRollback},
		{"删除与不存在可判定", checkDeleteVsNever},
		{"快照点边界左闭右开", checkBoundary},
		{"回收不改变活跃快照读结果", checkReclaimStable},
		{"增量回收考察数对照", checkIncremental},
		{"水位只升不降", checkWaterMonotonic},
		{"崩溃点遍历全部安全", checkCrashPoints},
		{"三类超限可判定", checkLimits},
		{"只读查询稳定", checkStatsStable},
		{"长事务不阻塞其他键", checkLongTx},
	}
	pass := 0
	for _, c := range checks {
		if c.run() {
			fmt.Printf("OK   %s\n", c.name)
			pass++
		} else {
			fmt.Printf("FAIL %s\n", c.name)
		}
	}
	fmt.Printf("总计 %d/%d 通过\n", pass, len(checks))
	if pass != len(checks) {
		os.Exit(1)
	}
}
