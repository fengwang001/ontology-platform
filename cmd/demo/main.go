package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/bq"
)

func okf(pass bool, format string, a ...any) {
	tag := "OK"
	if !pass {
		tag = "FAIL"
	}
	fmt.Printf("%s: %s\n", tag, fmt.Sprintf(format, a...))
}

// eightStep replays the NOTES.md capacity-5 scenario through the public API.
// Ops are +n Produce(n), -n Consume(-n); labels: ok / back / under.
func eightStep() bool {
	q, _ := api.New(5)
	ops := []int64{3, 2, 1, -2, 3, -1, 3, -6}
	want := []string{"ok", "ok", "back", "ok", "back", "ok", "ok", "under"}
	wantCount := []int64{3, 5, 5, 3, 3, 2, 5, 5}
	var b strings.Builder
	pass := true
	for i, n := range ops {
		got := "ok"
		if n > 0 {
			if ok, _ := q.Produce(n); !ok {
				got = "back"
			}
		} else if err := q.Consume(-n); errors.Is(err, api.ErrUnderflow) {
			got = "under"
		}
		if got != want[i] || q.Count() != wantCount[i] {
			pass = false
		}
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%d:%s", q.Count(), got)
	}
	okf(pass, "eight steps count:result (%s)", b.String())
	return pass
}

// invariantBundle: SelfCheck replays built-in sequences against a naive
// explicit-slice reference, checking reference equality, 0<=Count<=capacity
// and conservation after every step.
func invariantBundle() {
	q, _ := api.New(5)
	okf(q.SelfCheck(), "naive-reference match + bounds + conservation (SelfCheck)")
}

// errorKinds: the four failures must be mutually distinct sentinels.
func errorKinds() {
	_, e0 := api.New(0)
	q, _ := api.New(5)
	q.Produce(3)
	_, e1 := q.Produce(0)
	e2 := q.Consume(0)
	e3 := q.Consume(4) // count is 3
	errs := []error{e0, e1, e2, e3}
	want := []error{api.ErrInvalidCapacity, api.ErrInvalidProduce, api.ErrInvalidConsume, api.ErrUnderflow}
	pass := true
	for i := range errs {
		if !errors.Is(errs[i], want[i]) {
			pass = false
		}
		for j := i + 1; j < len(errs); j++ {
			if errors.Is(errs[i], errs[j]) {
				pass = false
			}
		}
	}
	okf(pass, "four distinct sentinel errors")
}

// rejectedNoTrace: rejected calls change nothing and the queue stays usable.
func rejectedNoTrace() {
	q, _ := api.New(5)
	q.Produce(3)
	before := q.Count()
	_, ep := q.Produce(-1)
	ec := q.Consume(0)
	eu := q.Consume(9)
	okBP, _ := q.Produce(5) // 3+5 > 5: clean backpressure
	classified := errors.Is(ep, api.ErrInvalidProduce) && errors.Is(ec, api.ErrInvalidConsume) &&
		errors.Is(eu, api.ErrUnderflow)
	untouched := q.Count() == before
	okAfter, _ := q.Produce(2)
	pass := classified && !okBP && untouched && okAfter &&
		q.Consume(5) == nil && q.Count() == 0
	okf(pass, "rejections/backpressure leave no trace; queue still usable")
}

// mScale: functional face of the unexported-counter test. Consume(1) after
// producing m must leave m-1 at every scale; the counter value itself is read
// only by the in-package white-box test (unexported field, never exported).
func mScale() {
	pass := true
	for _, m := range []int64{100, 1000, 10000} {
		c := bq.New(m)
		if !c.Produce(m) || !c.Consume(1) || c.Count() != m-1 || c.Free() != 1 {
			pass = false
		}
	}
	okf(pass, "m=100..10000 Consume(1) no linear move (counter: white-box test)")
}

// concurrentProduce: N goroutines each Produce(1); no sleeps.
func concurrentProduce() {
	const N = 500
	q, _ := api.New(N)
	var wg sync.WaitGroup
	var bad atomic.Bool
	for range N {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := q.Produce(1)
			c := q.Count()
			if !ok || err != nil || c < 0 || c > N {
				bad.Store(true)
			}
		}()
	}
	wg.Wait()
	okf(!bad.Load() && q.Count() == N, "concurrent %d x Produce(1) -> Count==%d", N, N)
}

func main() {
	eightStep()
	invariantBundle()
	errorKinds()
	rejectedNoTrace()
	mScale()
	concurrentProduce()
}
