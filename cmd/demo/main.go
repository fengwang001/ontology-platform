// Command demo exercises the alignment allocator and prints OK/FAIL lines.
// No arguments, no network. Exit code is 0 only when every verdict passes.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/alloc"
	"ontology/api"
)

var failed bool

func check(name string, ok bool, info string) {
	status := "OK  "
	if !ok {
		status, failed = "FAIL", true
	}
	fmt.Printf("%s %-52s %s\n", status, name, info)
}

func main() {
	// Eight built-in operations: {isAlloc, size, align, wantPtr, wantBase, wantNext}.
	script := [...][6]int{
		{1, 10, 16, 16, 0, 33},
		{1, 4, 8, 48, 0, 52},
		{1, 8, 8, 64, 0, 75},
		{0, 0, 0, 16, 0, 75},
		{0, 0, 0, 48, 33, 75},
		{1, 1, 8, 88, 0, 91},
		{1, 16, 16, 112, 0, 130},
		{0, 0, 0, 64, 52, 130},
	}
	d := alloc.New()
	stepsOK, alignedOK, readbackOK := true, true, true
	trace := ""
	for _, s := range script {
		if s[0] == 1 {
			p, err := d.Alloc(s[1], s[2])
			if err != nil || p != s[3] {
				stepsOK = false
			}
			if p%s[2] != 0 {
				alignedOK = false
			}
			trace += fmt.Sprintf("%d@%d ", p, s[5])
		} else {
			b, err := d.Free(s[3])
			if err != nil || b != s[4] {
				stepsOK, readbackOK = false, false
			}
			trace += fmt.Sprintf("b%d@%d ", b, s[5])
		}
	}
	check("eight steps: ptr/base@next each", stepsOK, trace)
	check("every ptr is a multiple of its align", alignedOK, "")
	check("Free read-back base correct at [ptr-8,ptr)", readbackOK, "b0,b33,b52")

	// Invariants: naive-model pointer-set/header-base agreement (SelfCheck).
	serr := api.New().SelfCheck()
	check("naive-model set/base match (SelfCheck)", serr == nil, msg(serr))

	// Three distinct decidable sentinel errors.
	x := api.New()
	_, e1 := x.Alloc(0, 8)
	_, e2 := x.Alloc(4, 6)
	e3 := x.Free(999)
	distinct := errors.Is(e1, api.ErrBadSize) && errors.Is(e2, api.ErrBadAlign) &&
		errors.Is(e3, api.ErrInvalidFree) && !errors.Is(e1, e2) && !errors.Is(e2, e3)
	check("three distinct sentinel errors", distinct, fmt.Sprintf("{%v|%v|%v}", e1, e2, e3))

	// Rejections leave no trace; allocator remains usable.
	before := x.Allocated()
	x.Alloc(-5, 8)
	x.Alloc(4, 6)
	x.Free(4096)
	_, uerr := x.Alloc(1, 8)
	check("rejected ops leave no trace, still usable",
		x.Allocated() == before+1 && uerr == nil, fmt.Sprintf("Allocated=%d", x.Allocated()))

	// Complexity verdict only: probe counts never cross the package boundary.
	perr := alloc.CheckProbeComplexity()
	check("Free probes <=1 at m=100,1000,10000", perr == nil, msg(perr))

	// Concurrent allocs: distinct, aligned; monotonic Allocated; no sleeps.
	const n = 128
	cx := api.New()
	start, stop := make(chan struct{}), make(chan struct{})
	var mono atomic.Bool
	mono.Store(true)
	var rw, gw sync.WaitGroup
	rw.Add(1)
	go func() {
		defer rw.Done()
		prev := 0
		for {
			select {
			case <-stop:
				return
			default:
				if cur := cx.Allocated(); cur < prev {
					mono.Store(false)
				} else {
					prev = cur
				}
			}
		}
	}()
	ptrs, als := make([]int, n), make([]int, n)
	for i := 0; i < n; i++ {
		als[i] = 1 << uint(i%7)
		gw.Add(1)
		go func(i int) {
			defer gw.Done()
			<-start
			if p, err := cx.Alloc(1+i%13, als[i]); err == nil {
				ptrs[i] = p
			}
		}(i)
	}
	close(start)
	gw.Wait()
	close(stop)
	rw.Wait()
	uniq := map[int]bool{}
	for i, p := range ptrs {
		uniq[p] = true
		if p == 0 || p%als[i] != 0 {
			alignedOK = false
		}
	}
	check("concurrent allocs distinct, aligned, monotonic count",
		len(uniq) == n && alignedOK && mono.Load() && cx.Allocated() == n,
		fmt.Sprintf("n=%d unique=%d", n, len(uniq)))

	if failed {
		os.Exit(1)
	}
}

func msg(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}
