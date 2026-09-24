package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/dag"
	"ontology/view"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

func build() *api.Engine {
	e := api.New()
	_ = e.AddView("A", nil, nil)
	_ = e.AddView("B", nil, nil)
	_ = e.AddView("E", []string{"C", "D"}, func(a ...int64) int64 { return a[0] + a[1] })
	_ = e.AddView("F", []string{"D"}, func(a ...int64) int64 { return a[0] - 1 })
	_ = e.AddView("C", []string{"A", "B"}, func(a ...int64) int64 { return a[0] + a[1] })
	_ = e.AddView("D", []string{"A"}, func(a ...int64) int64 { return a[0] * 2 })
	return e
}

func main() {
	g := dag.New()
	_ = g.Add("M", []string{"N"}) // 前向引用，允许
	err := g.Add("N", []string{"M"})
	check("dag: M<->N cycle rejected", errors.Is(err, dag.ErrCycle) && !g.Has("N"))

	st := view.New()
	evals := 0
	_ = st.Add("A", nil, nil)
	_ = st.Add("B", nil, nil)
	_ = st.Add("C", []string{"A", "B"}, func(a ...int64) int64 { evals++; return a[0] + a[1] })
	st.Set("A", 1)
	st.Set("B", 2) // C 被多处失效，仍只记一次脏
	err = st.Recompute()
	cv, _ := st.Get("C")
	check("view: topo recompute + dedup", err == nil && cv == 3 && evals == 1)

	e := build()
	_ = e.Set("A", 1)
	_ = e.Set("B", 2)
	_ = e.Recompute()
	_ = e.Set("B", 20)
	_ = e.Recompute()
	_ = e.Set("A", 10)
	_ = e.Recompute()
	ev, _, _ := e.Get("E")
	fv, _, _ := e.Get("F")
	check("api: section3 final E=50 F=19", ev == 50 && fv == 19)

	e2 := api.New()
	_ = e2.AddView("M", []string{"N"}, nil)
	errM := e2.AddView("N", []string{"M"}, nil)
	check("api: cycle M<->N rejected", errors.Is(errM, dag.ErrCycle))

	e3 := api.New()
	_ = e3.AddView("A", nil, nil)
	errs := []error{e3.AddView("", nil, nil), e3.AddView("A", nil, nil),
		e3.Set("zz", 1), e2.Recompute()}
	want := []error{api.ErrEmptyName, api.ErrDuplicateName, api.ErrUnknownView, view.ErrUnresolved}
	ok := true
	for i := range errs {
		ok = ok && errors.Is(errs[i], want[i])
	}
	check("api: 4 distinct decidable errors", ok)

	before, _, _ := e.Get("E")
	_ = e.AddView("", nil, nil)
	_ = e.AddView("A", nil, nil)
	_ = e.Set("nobody", 1)
	after, _, _ := e.Get("E")
	ok = before == after && e.Recompute() == nil
	check("api: rejected ops leave state unchanged", ok)

	// 大 m：仅 Set(X) 后求值数不随 m 增长（fn 副作用计数）
	count := 0
	e4 := api.New()
	for i := 0; i < 10000; i++ {
		_ = e4.AddView(fmt.Sprintf("W%d", i), nil, nil)
	}
	_ = e4.AddView("X", nil, nil)
	_ = e4.AddView("Y", []string{"X"}, func(a ...int64) int64 { count++; return a[0] + 1 })
	_ = e4.AddView("Z", []string{"Y"}, func(a ...int64) int64 { count++; return a[0] + 1 })
	_ = e4.Set("X", 1)
	_ = e4.Recompute()
	check("api: eval count independent of m", count == 2)

	// 并发：1 写者 Set+Recompute，8 读者 Get，读到的值必须合法
	e5 := api.New()
	_ = e5.AddView("A", nil, nil)
	_ = e5.AddView("D", []string{"A"}, func(a ...int64) int64 { return a[0] * 2 })
	const k = 200
	legal := map[int64]bool{0: true}
	for i := 1; i <= k; i++ {
		legal[int64(2*i)] = true
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	bad := make(chan int64, 64)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < k; j++ {
				if v, _, err := e5.Get("D"); err == nil && !legal[v] {
					bad <- v
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 1; i <= k; i++ {
			_ = e5.Set("A", int64(i))
			_ = e5.Recompute()
		}
	}()
	close(start)
	wg.Wait()
	last, _, _ := e5.Get("D")
	check("api: concurrent reads legal", len(bad) == 0 && last == 2*k)

	check("api: SelfCheck", api.New().SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
