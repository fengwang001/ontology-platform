// Command demo 逐条演示熔断与舱壁隔离调用保护器的验收判定。
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"ontology/classify"
	"ontology/stat"
)

type check struct {
	name string
	run  func() bool
}

func main() {
	checks := []check{
		{"错误分类可用 errors.Is 区分", checkClassify},
		{"统计计数器三条等式自洽", checkStat},
	}
	passed := 0
	for _, c := range checks {
		ok := c.run()
		verdict := "OK  "
		if !ok {
			verdict = "FAIL"
		}
		if ok {
			passed++
		}
		fmt.Printf("%s %s\n", verdict, c.name)
	}
	fmt.Printf("TOTAL %d/%d OK\n", passed, len(checks))
	if passed != len(checks) {
		os.Exit(1)
	}
}

func checkStat() bool {
	var rec stat.Recorder
	for i := 0; i < 100; i++ {
		rec.RecordTotal()
		switch i % 5 {
		case 0:
			rec.RecordReal()
			rec.RecordSuccess()
		case 1:
			rec.RecordReal()
			rec.RecordFailure(classify.Kind(i % 3))
		case 2:
			rec.RecordBreakerReject()
		case 3:
			rec.RecordBulkheadReject()
		default:
			rec.RecordReal()
			rec.RecordFailure(classify.Timeout)
		}
	}
	return len(rec.Snapshot().Invariants()) == 0
}

func checkClassify() bool {
	timeoutErr := fmt.Errorf("rpc: %w", context.DeadlineExceeded)
	nonRetry := classify.MarkNonRetryable(errors.New("bad request"))
	retry := classify.MarkRetryable(errors.New("connection reset"))
	return classify.Of(timeoutErr) == classify.Timeout &&
		classify.Of(nonRetry) == classify.NonRetryable &&
		classify.Of(retry) == classify.Retryable &&
		classify.Of(errors.New("unknown")) == classify.Retryable &&
		errors.Is(nonRetry, classify.ErrNonRetryable) &&
		errors.Is(retry, classify.ErrRetryable) &&
		errors.Is(timeoutErr, context.DeadlineExceeded)
}
