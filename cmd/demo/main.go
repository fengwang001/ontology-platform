package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"ontology/bulkhead"
	"ontology/classify"
)

type check struct {
	name string
	ok   bool
}

func main() {
	checks := []check{
		{"errors.Is distinguishes four classes",
			errors.Is(classify.Retryable(nil), classify.ErrRetryable) &&
				errors.Is(classify.NonRetryable(nil), classify.ErrNonRetryable) &&
				errors.Is(classify.Timeout(nil), classify.ErrTimeout) &&
				classify.Of(nil) == classify.ClassSuccess},
		{"inflight peak <= N", func() bool {
			b, _ := bulkhead.New(8, 500)
			gate := make(chan struct{})
			var wg sync.WaitGroup
			for i := 0; i < 500; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-gate
					rel, err := b.Acquire(context.Background())
					if err != nil {
						return
					}
					time.Sleep(time.Millisecond)
					rel()
				}()
			}
			close(gate)
			wg.Wait()
			return b.Peak() <= 8 && b.Available() == 8
		}()},
		{"queue full rejects immediately", func() bool {
			b, _ := bulkhead.New(1, 0)
			rel, _ := b.Acquire(context.Background())
			defer rel()
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			start := time.Now()
			_, err := b.Acquire(ctx)
			return errors.Is(err, bulkhead.ErrBulkheadFull) && time.Since(start) < 10*time.Millisecond
		}()},
		{"downstream X exhaustion spares Y", func() bool {
			x, _ := bulkhead.New(1, 0)
			y, _ := bulkhead.New(2, 0)
			rx, _ := x.Acquire(context.Background())
			defer rx()
			_, xerr := x.Acquire(context.Background())
			return errors.Is(xerr, bulkhead.ErrBulkheadFull) && y.Available() == 2
		}()},
	}
	pass := 0
	for _, c := range checks {
		if c.ok {
			pass++
			fmt.Printf("OK %s\n", c.name)
		} else {
			fmt.Printf("FAIL %s\n", c.name)
		}
	}
	fmt.Printf("TOTAL %d/%d OK\n", pass, len(checks))
	if pass != len(checks) {
		os.Exit(1)
	}
}
