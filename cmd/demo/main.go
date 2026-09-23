// demo 演示带撤回的增量聚合视图维护器：逐项检查并打印 OK/FAIL。
package main

import (
	"fmt"
	"os"
)

var failures int

func report(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

func main() {
	dir, err := os.MkdirTemp("", "ontology-demo")
	if err != nil {
		fmt.Println("FAIL 无法创建临时目录:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)
	report("Min重算=3且Sum=0", checkRecomputeCounts())
	report("增量==全量重算(位级)", checkAuditEqual())
	report("删空组后查询不存在", checkGroupGone())
	report("逐字节截断四类分类", checkTruncation(dir))
	report("乱序变更被拒计数", checkStaleRejected())
	report("三崩溃点恢复一致", checkCrashRecovery(dir))
	report("并发提交聚合正确", checkConcurrency())
	total := 7
	fmt.Printf("总计 %d 项：%d OK / %d FAIL\n", total, total-failures, failures)
	if failures > 0 {
		os.Exit(1)
	}
}
