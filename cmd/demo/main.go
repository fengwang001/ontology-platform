// Command demo 演示结构化日志脱敏与一致性采样器的各项判定。
package main

import (
	"errors"
	"fmt"

	"ontology/record"
)

var failures int

func check(name string, ok bool, detail string) {
	status := "OK  "
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s: %s\n", status, name, detail)
}

func main() {
	checkRecord()
	fmt.Printf("TOTAL %d failure(s)\n", failures)
}

// checkRecord 判定：深度超限被拒并计数、循环引用被检出。
func checkRecord() {
	deep := map[string]any{}
	cur := deep
	for i := 0; i < record.MaxDepth+5; i++ {
		next := map[string]any{}
		cur["n"] = next
		cur = next
	}
	before := record.DepthRejections()
	_, err := record.New("t", record.Info, deep)
	ok := errors.Is(err, record.ErrDepth) && record.DepthRejections() > before
	check("depth-limit", ok, fmt.Sprintf("err=%v rejections=%d", err, record.DepthRejections()))

	cyc := map[string]any{}
	cyc["self"] = cyc
	_, err = record.New("t", record.Info, cyc)
	check("cycle-detect", errors.Is(err, record.ErrCycle), fmt.Sprintf("err=%v", err))
}
