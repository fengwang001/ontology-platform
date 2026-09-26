package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sync"

	"ontology/api"
	"ontology/sm"
)

var failed bool

func step(name string, a *api.API, c, l, s int) {
	ok := a.Committed() == c && a.LastApplied() == l && a.State() == s
	report(fmt.Sprintf("%s committed=%d lastApplied=%d state=%d (want %d,%d,%d)",
		name, a.Committed(), a.LastApplied(), a.State(), c, l, s), ok)
}

func report(name string, ok bool) {
	if ok {
		fmt.Println("OK:  ", name)
	} else {
		failed = true
		fmt.Println("FAIL:", name)
	}
}

func main() {
	// 第三节五步。
	a := api.New()
	for _, c := range []sm.Command{{Op: sm.Add, K: 2}, {Op: sm.Mul, K: 3}, {Op: sm.Add, K: 1}, {Op: sm.Add, K: 5}} {
		if err := a.Append(c); err != nil {
			panic(err)
		}
	}
	step("S1 append x4", a, 0, 0, 0)
	must(a.Commit(3))
	step("S2 commit(3)", a, 3, 0, 0)
	a.Apply()
	step("S3 apply", a, 3, 3, 7)
	must(a.Restart(2, 6))
	must(a.Commit(4))
	step("S4 restart(2,6)+commit(4)", a, 4, 2, 6)
	a.Apply()
	step("S5 apply", a, 4, 4, 12)

	// 分批 Apply 不影响最终状态。
	batch, once := api.New(), api.New()
	rr := rand.New(rand.NewSource(7))
	for i := 0; i < 100; i++ {
		c := sm.Command{Op: sm.Add, K: rr.Intn(5)}
		if rr.Intn(2) == 1 {
			c = sm.Command{Op: sm.Mul, K: rr.Intn(4) + 1}
		}
		_ = batch.Append(c)
		_ = once.Append(c)
	}
	for c := 0; c < 100; {
		c += 1 + rr.Intn(6)
		if c > 100 {
			c = 100
		}
		_ = batch.Commit(c)
		batch.Apply()
	}
	_ = once.Commit(100)
	once.Apply()
	report("batched apply equals one-shot apply", batch.State() == once.State())

	// 三类可判定错误且互不相同；被拒后状态不变。
	z := api.New()
	ec, ea, es := z.Commit(1), z.Append(sm.Command{}), z.Restart(1, 1)
	distinct := errors.Is(ec, api.ErrCommitOutOfRange) && errors.Is(ea, api.ErrEmptyCommand) &&
		errors.Is(es, api.ErrSnapOutOfRange) && ec != ea && ea != es
	untouched := z.Committed() == 0 && z.LastApplied() == 0 && z.State() == 0
	report("three distinct sentinel errors, state untouched", distinct && untouched)

	// readCount 非导出，O(1) 续读只能经同包自检判定，不经过任何导出接口。
	report("selfcheck: 4 invariants & readCount=1 at m=100..10000", a.SelfCheck() == nil)

	// 并发只读：N 个 goroutine 读到的三元组逐字段相同（无 sleep）。
	must(z.Append(sm.Command{Op: sm.Add, K: 9}))
	must(z.Commit(1))
	z.Apply()
	const N = 16
	type triple struct{ s, l, c int }
	res := make(chan triple, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); res <- triple{z.State(), z.LastApplied(), z.Committed()} }()
	}
	wg.Wait()
	close(res)
	first := <-res
	agree := true
	for t := range res {
		if t != first {
			agree = false
		}
	}
	report("16 concurrent readers see identical triple", agree)

	if failed {
		os.Exit(1)
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
