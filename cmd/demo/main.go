// Command demo 逐条演示熔断与舱壁隔离调用保护器的关键判定。
package main

import (
	"errors"
	"fmt"

	"ontology/classify"
)

var passed, failed int

func check(name string, ok bool) {
	if ok {
		passed++
		fmt.Printf("OK   %s\n", name)
		return
	}
	failed++
	fmt.Printf("FAIL %s\n", name)
}

func main() {
	demoClassify()
	fmt.Printf("TOTAL %d OK, %d FAIL\n", passed, failed)
}

func demoClassify() {
	err := errors.New("boom")
	ok := classify.Of(err) == classify.Retryable &&
		classify.Of(classify.Mark(err)) == classify.NonRetryable &&
		errors.Is(classify.Mark(err), classify.ErrNonRetryable)
	check("classify: 默认可重试/标记不可重试/errors.Is 可判定", ok)
}
