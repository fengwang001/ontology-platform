// Command demo prints one OK/FAIL line per deliverable check and exits
// non-zero if any check fails.
package main

import (
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/dpq"
	"ontology/mmheap"
)

var failed bool

func report(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Println(map[bool]string{true: "OK  ", false: "FAIL"}[ok], name)
}

func main() {
	report("eight-op trace ends at {1,5}, Min=1 Max=5", trace())
	report("SelfCheck: naive consistency / heap structure / conservation / no-trace", api.New().SelfCheck() == nil)
	report("negative and duplicate values", negDup())
	report("empty-queue Min/Max/DeleteMin/DeleteMax rejected, state unchanged", empty())
	report("op cost independent of m (O(log m) path)", scale())
	report("concurrent Push: Min/Max/Len correct", concurrent())
	if failed {
		os.Exit(1)
	}
}

// trace replays the eight prescribed operations: op 0=Push(arg),
// 1=DeleteMin, 2=DeleteMax; ret is the expected Delete return, mn/mx the
// expected Min/Max after the step.
func trace() bool {
	steps := []struct{ op, arg, ret, mn, mx int64 }{
		{0, 5, 0, 5, 5}, {0, 8, 0, 5, 8}, {0, 3, 0, 3, 8}, {0, 8, 0, 3, 8},
		{1, 0, 3, 5, 8}, {0, 1, 0, 1, 8}, {2, 0, 8, 1, 8}, {2, 0, 8, 1, 5},
	}
	q := api.New()
	for _, s := range steps {
		got := int64(0)
		switch s.op {
		case 0:
			q.Push(s.arg)
		case 1:
			got, _ = q.DeleteMin()
		case 2:
			got, _ = q.DeleteMax()
		}
		mn, _ := q.Min()
		mx, _ := q.Max()
		if s.op != 0 && got != s.ret || mn != s.mn || mx != s.mx {
			return false
		}
	}
	return q.Len() == 2
}

// negDup checks Min/Max/DeleteMin ordering with negative and duplicated
// values by draining the queue into a sorted sequence.
func negDup() bool {
	q := api.New()
	for _, v := range []int64{-7, 3, -7, 0, 3, -2} {
		q.Push(v)
	}
	if mn, _ := q.Min(); mn != -7 {
		return false
	}
	if mx, _ := q.Max(); mx != 3 {
		return false
	}
	want := []int64{-7, -7, -2, 0, 3, 3}
	for _, w := range want {
		if v, _ := q.DeleteMin(); v != w {
			return false
		}
	}
	return q.Len() == 0
}

// empty probes all four operations on an empty queue: each must return
// ok=false, Len must stay 0, and the queue must work afterwards.
func empty() bool {
	var q dpq.Queue
	ok := true
	probe := func(got int64, o bool) { ok = ok && got == 0 && !o && q.Len() == 0 }
	probe(q.Min())
	probe(q.Max())
	probe(q.DeleteMin())
	probe(q.DeleteMax())
	q.Push(9)
	v, o := q.Min()
	return ok && o && v == 9 && q.Len() == 1
}

// scale fills heaps of growing size m and checks that one more
// Push/DeleteMin/DeleteMax stays within O(log m) inspected nodes.
func scale() bool {
	for _, m := range []int{100, 300, 1000, 3000, 10000} {
		var h mmheap.Heap
		for i := 0; i < m; i++ {
			h.Push(int64(i * 37 % 101))
		}
		h.Push(-1000) // worst case: new global min bubbles the full path
		ok := h.ScaleOK(m)
		h.DeleteMin()
		ok = ok && h.ScaleOK(m)
		h.DeleteMax()
		if !ok || !h.ScaleOK(m) {
			return false
		}
	}
	return true
}

// concurrent has N goroutines push distinct values, then checks the
// resulting Min/Max/Len. No sleeps; WaitGroup only.
func concurrent() bool {
	const n = 256
	var q dpq.Queue
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(v int64) { defer wg.Done(); q.Push(v) }(int64(i))
	}
	wg.Wait()
	mn, ok1 := q.Min()
	mx, ok2 := q.Max()
	return ok1 && ok2 && mn == 0 && mx == n-1 && q.Len() == n
}
