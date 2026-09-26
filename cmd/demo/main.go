// Command demo exercises the bounded queue + backpressure and prints OK/FAIL.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync/atomic"

	"ontology/api"
	"ontology/bp"
)

var allOK = true

func check(name string, cond bool) {
	status := "OK  "
	if !cond {
		status, allOK = "FAIL ", false
	}
	fmt.Println(status + name)
}

// eight runs the section-3 program (capacity 5), prints the actual per-step
// count/result and reports whether it matches the derivation.
func eight() bool {
	q, _ := api.New(5)
	ops := [8]int64{3, 2, 1, -2, 3, -1, 3, -6}
	wantCnt := [8]int64{3, 5, 5, 3, 3, 2, 5, 5}
	wantRes := [8]string{"成功", "成功", "背压", "成功", "背压", "成功", "成功", "错误"}
	mark := map[string]string{"成功": "✓", "背压": "满", "错误": "溢"}
	ok := true
	fmt.Print("八步(数量/结果):")
	for i, n := range ops {
		res := "成功"
		if n > 0 {
			placed, err := q.Produce(n)
			if err != nil {
				res = "错误"
			} else if !placed {
				res = "背压"
			}
		} else if q.Consume(-n) != nil {
			res = "错误"
		}
		if q.Count() != wantCnt[i] || res != wantRes[i] {
			ok = false
		}
		fmt.Printf(" %d%s", q.Count(), mark[res])
	}
	fmt.Println()
	return ok
}

// mixed replays a varied program vs a naive slice; returns invariants 1-3.
func mixed() (naive, bounds, conserve bool) {
	q, _ := api.New(7)
	var ref []int
	var sp, sc int64
	x := int64(7)
	naive, bounds, conserve = true, true, true
	for k := 0; k < 200; k++ {
		x = (x*6364136223846793005 + 1442695040888963407) & 0x7fffffffffffffff
		n := x%5 - 1
		if x&1 == 0 {
			if placed, _ := q.Produce(n); placed {
				ref = append(ref, make([]int, n)...)
				sp += n
			}
		} else if q.Consume(n) == nil {
			ref = ref[n:]
			sc += n
		}
		c := q.Count()
		if int64(len(ref)) != c {
			naive = false
		}
		if c < 0 || c > 7 {
			bounds = false
		}
		if c != sp-sc {
			conserve = false
		}
	}
	return
}

// faults exercises the four distinct sentinel errors and continued usability.
func faults() (distinct, reusable bool) {
	_, e0 := api.New(0)
	q, _ := api.New(5)
	q.Produce(2)
	_, e1 := q.Produce(0)
	e2 := q.Consume(0)
	e3 := q.Consume(3) // only 2 held -> underflow
	distinct = errors.Is(e0, bp.ErrBadCapacity) && errors.Is(e1, bp.ErrIllegalProduce) &&
		errors.Is(e2, bp.ErrIllegalConsume) && errors.Is(e3, bp.ErrUnderflow)
	noTrace := q.Count() == 2
	placed, _ := q.Produce(3)
	reusable = noTrace && placed && q.Count() == 5
	return
}

// bigm observes O(1) Consume(1) at large depths; moved==0 is white-box pinned.
func bigm() bool {
	for _, m := range []int64{100, 1000, 10000} {
		q, _ := api.New(m)
		if p, _ := q.Produce(m); !p || q.Consume(1) != nil || q.Count() != m-1 {
			return false
		}
	}
	return true
}

// concurrent has N producers each place 1 while the main goroutine reads;
// every reading stays in [0,n] and the final count is exactly n (no sleeps).
func concurrent() bool {
	const n = 200
	q, _ := api.New(n)
	var done atomic.Int32
	for i := 0; i < n; i++ {
		go func() {
			if placed, _ := q.Produce(1); placed {
				done.Add(1)
			}
		}()
	}
	for done.Load() < n {
		if c := q.Count(); c < 0 || c > n {
			return false
		}
	}
	return q.Count() == n
}

func main() {
	check("八步数量与结果", eight())
	naive, bounds, conserve := mixed()
	check("与朴素参照一致", naive)
	check("容量不越界", bounds)
	check("守恒 ΣP-ΣC", conserve)
	distinct, reusable := faults()
	check("四类可判定错误互不相同", distinct)
	check("被拒后状态不变且可继续", reusable)
	check("大m下Consume(1)为O(1)(私有计数器白盒钉住)", bigm())
	check("并发Produce后Count正确且全程不越界", concurrent())
	if !allOK {
		os.Exit(1)
	}
}
