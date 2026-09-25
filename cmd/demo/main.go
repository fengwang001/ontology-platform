// Command demo 逐条演示熔断与舱壁隔离调用保护器的判定项。
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"ontology/classify"
	"ontology/timeout"
)

type check struct {
	name string
	run  func() bool
}

func main() {
	checks := []check{
		{"超时与panic被正确分类", checkClassifyTimeout},
	}
	failed := 0
	for _, c := range checks {
		verdict := "OK"
		if !c.run() {
			verdict = "FAIL"
			failed++
		}
		fmt.Printf("%s %s\n", verdict, c.name)
	}
	fmt.Printf("TOTAL %d/%d passed\n", len(checks)-failed, len(checks))
	if failed > 0 {
		os.Exit(1)
	}
}

func checkClassifyTimeout() bool {
	err := timeout.Do(context.Background(), 5*time.Millisecond, func(context.Context) error {
		time.Sleep(100 * time.Millisecond)
		return nil
	})
	if !errors.Is(err, classify.ErrTimeout) || classify.Of(err) != classify.Timeout {
		return false
	}
	pErr := timeout.Do(context.Background(), time.Second, func(context.Context) error { panic("x") })
	return pErr != nil && classify.Of(pErr) == classify.Retryable
}
