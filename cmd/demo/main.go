// Command demo 演示 GROUPING SETS 增量聚合。不读参数、不联网，退出码 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/agg"
	"ontology/api"
	"ontology/gset"
)

func fail(line string) {
	fmt.Println(line)
	os.Exit(1)
}

func main() {
	ga, _ := gset.NewGroup([]int{0})
	gab, _ := gset.NewGroup([]int{0, 1})
	g0, _ := gset.NewGroup(nil)
	if ga.ID() != 3 || gab.ID() != 1 || g0.ID() != 7 || gab.Grouping(2) != 1 {
		fail("FAIL group ids/grouping")
	}
	fmt.Println("OK group IDs (A)=3 (A,B)=1 ()=7; GROUPING(C) of (A,B)=1")

	v, err := api.New([][]int{{0}, {0, 1}, nil})
	if err != nil {
		fail("FAIL New: " + err.Error())
	}
	seq := []api.Fact{
		{A: "x", B: "p", C: "u", M: 10}, {A: "x", B: "p", C: "v", M: 20}, {A: "x", B: "q", C: "u", M: 5},
		{A: "y", B: "p", C: "u", M: 7}, {A: "x", B: "p", C: "v", M: -20}, {A: "y", B: "p", C: "u", M: 3},
		{A: "x", B: "p", C: "u", M: -10}, {A: "x", B: "q", C: "u", M: 2},
	}
	grand := []int64{10, 30, 35, 42, 22, 25, 15, 17}
	var got string
	for i, f := range seq {
		if err := v.Apply(f); err != nil {
			fail(fmt.Sprintf("FAIL apply step %d: %v", i+1, err))
		}
		if v.View()[7][""] != grand[i] {
			fail(fmt.Sprintf("FAIL grand sum step %d", i+1))
		}
		got += fmt.Sprintf("%d ", grand[i])
	}
	fmt.Println("OK group () sum after each of 8 steps:", got)
	if _, present := v.View()[1]["x\x1fp"]; present {
		fail("FAIL zero entry (x,p) retained after step 7")
	}
	fmt.Println("OK step 7 removes (A,B) key (x,p) as it reaches 0")

	if err := agg.CheckProbeConstant(100, 1000, 10000); err != nil {
		fail("FAIL probe: " + err.Error())
	}
	fmt.Println("OK probe count stays constant for m in {100,1000,10000}")

	before := v.View()
	bad := []error{
		v.Apply(api.Fact{B: "p", C: "u", M: 1}),            // A 为空
		v.Apply(api.Fact{A: "x", B: "p", C: "u"}),          // M==0
		v.Apply(api.Fact{A: "x", B: "q", C: "u", M: -100}), // 撤回越界
		func() error { _, e := api.New(nil); return e }(),  // 分组集非法
	}
	want := []error{api.ErrEmptyDim, api.ErrZeroMeasure, api.ErrWithdrawn, api.ErrInvalidGroups}
	for i, e := range bad {
		if !errors.Is(e, want[i]) || !reflect.DeepEqual(v.View(), before) {
			fail(fmt.Sprintf("FAIL error case %d: %v", i, e))
		}
	}
	if !distinct(want) {
		fail("FAIL sentinel errors not distinct")
	}
	fmt.Println("OK four distinct decidable errors; all rejected with state unchanged")

	const n = 16
	views := make([]map[int]map[string]int64, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) { defer wg.Done(); views[i] = v.View() }(i)
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		if !reflect.DeepEqual(views[0], views[i]) {
			fail(fmt.Sprintf("FAIL concurrent reader %d diverged", i))
		}
	}
	fmt.Println("OK 16 concurrent readers see identical views")

	if err := api.SelfCheck(); err != nil {
		fail("FAIL SelfCheck: " + err.Error())
	}
	fmt.Println("OK SelfCheck")
}

func distinct(es []error) bool {
	for i := range es {
		for j := i + 1; j < len(es); j++ {
			if es[i] == es[j] {
				return false
			}
		}
	}
	return true
}
