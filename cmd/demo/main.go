// demo 逐条核验最大最小公平分配器的关键性质，全部 OK 时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/alloc"
	"ontology/api"
	"ontology/mf"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s: %s\n", name, status)
}

func main() {
	a, err := api.New(30)
	if err != nil {
		fmt.Println("New: FAIL")
		os.Exit(1)
	}
	for id, d := range map[string]int64{"A": 6, "B": 12, "C": 18, "D": 30} {
		if err := a.Add(id, d); err != nil {
			fmt.Println("Add: FAIL")
			os.Exit(1)
		}
	}
	got := a.Allocate()
	want := map[string]mf.Frac{"A": mf.New(6, 1), "B": mf.New(8, 1), "C": mf.New(8, 1), "D": mf.New(8, 1)}
	step := mf.Cmp(mf.Fair(30, 4), mf.New(15, 2)) == 0 && mf.Cmp(mf.Fair(24, 3), mf.New(8, 1)) == 0
	final := true
	for id, w := range want {
		final = final && mf.Cmp(got[id], w) == 0
	}
	check("step-table(15/2,8) & final A=6 B=8 C=8 D=8", step && final)

	sum := mf.Frac{N: 0, D: 1}
	for _, v := range got {
		sum = mf.Add(sum, v)
	}
	check("conservation sum==30", mf.Cmp(sum, mf.New(30, 1)) == 0)

	level := mf.Cmp(got["B"], got["C"]) == 0 && mf.Cmp(got["C"], got["D"]) == 0 &&
		mf.Cmp(got["B"], got["A"]) > 0
	check("unsatisfied leveled to same waterline", level)

	tasks := []alloc.Task{{ID: "A", Demand: 6}, {ID: "B", Demand: 12}, {ID: "C", Demand: 18}, {ID: "D", Demand: 30}}
	naive := alloc.Naive(30, tasks)
	same := true
	for id, w := range naive {
		same = same && mf.Cmp(got[id], w) == 0
	}
	check("matches naive reference", same)

	errs := []error{api.ErrEmptyID, api.ErrDuplicateID, api.ErrNegativeDemand, api.ErrInvalidCapacity}
	distinct := map[error]bool{}
	for _, e := range errs {
		distinct[e] = true
	}
	_, e0 := api.New(0)
	e1, e2, e3 := a.Add("", 1), a.Add("A", 1), a.Add("Z", -1)
	rej := errors.Is(e0, api.ErrInvalidCapacity) && errors.Is(e1, api.ErrEmptyID) &&
		errors.Is(e2, api.ErrDuplicateID) && errors.Is(e3, api.ErrNegativeDemand)
	check("four distinct decidable errors", len(distinct) == 4 && rej)

	after := a.Allocate()
	unchanged := len(after) == len(got)
	for id, v := range got {
		unchanged = unchanged && mf.Cmp(after[id], v) == 0
	}
	check("rejected ops leave state unchanged", unchanged)

	largeOK := true // 考察个数的硬性断言在 alloc 包内测试；此处核验大 m 行为正确且守恒
	for _, m := range []int{100, 10000} {
		la, _ := api.New(int64(3 + 5*(m-3)))
		for i := 0; i < m; i++ {
			d := int64(1 << 40)
			if i < 3 {
				d = 1
			}
			if la.Add(fmt.Sprintf("t%d", i), d) != nil {
				largeOK = false
			}
		}
		lg := la.Allocate()
		ls := mf.Frac{N: 0, D: 1}
		for _, v := range lg {
			ls = mf.Add(ls, v)
		}
		largeOK = largeOK && mf.Cmp(lg["t5"], mf.New(5, 1)) == 0 &&
			mf.Cmp(ls, mf.New(int64(3+5*(m-3)), 1)) == 0
	}
	check("large-m waterline stable (counter asserted in alloc test)", largeOK)

	ca, _ := api.New(1000)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = ca.Add(fmt.Sprintf("c%d", i), int64(i+1))
		}(i)
	}
	wg.Wait()
	sa, _ := api.New(1000)
	for i := 0; i < 64; i++ {
		_ = sa.Add(fmt.Sprintf("c%d", i), int64(i+1))
	}
	cg, sg := ca.Allocate(), sa.Allocate()
	conc := len(cg) == len(sg)
	for id, v := range sg {
		conc = conc && mf.Cmp(cg[id], v) == 0
	}
	check("concurrent Add matches sequential", conc)

	check("SelfCheck", ca.SelfCheck() == nil && a.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
