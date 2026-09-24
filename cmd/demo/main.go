// Command demo 对有界重排窗口保序并行映射器做冒烟判定。
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"slices"
	"time"

	"ontology/check"
	"ontology/pmap"
)

var pass, total int

func report(name string, ok bool) {
	total++
	word := "OK"
	if !ok {
		word = "FAIL"
	} else {
		pass++
	}
	fmt.Printf("%s %s\n", word, name)
}

func main() {
	ctx := context.Background()
	ins := make([]int, 100)
	for i := range ins {
		ins[i] = i
	}
	double := func(_ context.Context, x int) (int, error) { return 2 * x, nil }
	var got []int
	emit := func(_, out int) { got = append(got, out) }
	err := pmap.Run(ctx, ins, 4, 4, double, emit)
	want, _ := check.Naive(ctx, ins, double)
	report("order matches naive", err == nil && slices.Equal(got, want))
	report("window bound <= 4", pmap.LastMaxHeld() <= 4)
	boom := errors.New("boom")
	fail := func(_ context.Context, x int) (int, error) {
		if x == 7 {
			return 0, boom
		}
		return x, nil
	}
	got = nil
	err = pmap.Run(ctx, ins, 4, 4, fail, emit)
	report("fail returns wrapped err", errors.Is(err, boom))
	report("fail emits prefix 0..6", slices.Equal(got, []int{0, 1, 2, 3, 4, 5, 6}))
	err = pmap.Run(ctx, ins, 0, 4, double, emit)
	report("bad config", errors.Is(err, pmap.ErrBadConfig))
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	err = pmap.Run(cctx, ins, 2, 2, double, emit)
	report("ctx cancel", errors.Is(err, context.Canceled))
	base := runtime.NumGoroutine()
	pmap.Run(ctx, ins, 8, 4, func(_ context.Context, x int) (int, error) {
		time.Sleep(time.Millisecond)
		return x, nil
	}, emit)
	settled := false
	for i := 0; i < 2000 && !settled; i++ {
		settled = runtime.NumGoroutine() <= base
		time.Sleep(time.Millisecond)
	}
	report("goroutines back to baseline", settled)
	fmt.Printf("%s %d/%d checks passed\n", map[bool]string{true: "OK", false: "FAIL"}[pass == total], pass, total)
	if pass != total {
		os.Exit(1)
	}
}
