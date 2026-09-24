// Command demo verifies the two-level LWW-Map CRDT end to end and prints at
// most ten OK/FAIL lines. It takes no arguments and does no networking.
package main

import (
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/omap"
)

var failed bool

func line(ok bool, format string, args ...any) {
	tag := "OK "
	if !ok {
		tag = "FAIL "
		failed = true
	}
	fmt.Print(tag)
	fmt.Printf(format+"\n", args...)
}

func put(o, i string, v int, ts int64, rep string) api.Op {
	return api.Op{Kind: api.Put, Outer: o, Inner: i, Value: v, TS: ts, Rep: rep}
}

func snap(a *api.API, o string) string {
	t, _ := a.Tomb(o)
	s := fmt.Sprintf("t%d ", t)
	v := a.View()[o]
	if len(v) == 0 {
		return s + "-"
	}
	first := true
	for _, k := range []string{"k1", "k2", "k3", "k4", "k5", "k6"} {
		if val, ok := v[k]; ok {
			if !first {
				s += ","
			}
			s += fmt.Sprintf("%s=%d", k, val)
			first = false
		}
	}
	return s
}

func main() {
	A, B := api.New(), api.New()
	steps := make([]string, 8)
	apply := func(r *api.API, op api.Op) {
		if err := r.Apply(op); err != nil {
			panic(err)
		}
	}
	apply(A, put("o", "k1", 100, 1, "A"))
	steps[0] = snap(A, "o")
	apply(A, put("o", "k2", 200, 2, "A"))
	steps[1] = snap(A, "o")
	apply(A, api.Op{Kind: api.DelOuter, Outer: "o", TS: 3})
	steps[2] = snap(A, "o")
	apply(B, put("o", "k3", 300, 1, "B"))
	steps[3] = snap(B, "o")
	apply(B, put("o", "k4", 400, 2, "B"))
	steps[4] = snap(B, "o")
	A.Merge(B)
	steps[5] = snap(A, "o")
	apply(A, put("o", "k5", 500, 3, "C"))
	steps[6] = snap(A, "o")
	apply(A, put("o", "k6", 600, 4, "D"))
	steps[7] = snap(A, "o")
	want := []string{"t0 k1=100", "t0 k1=100,k2=200", "t3 -", "t0 k3=300",
		"t0 k3=300,k4=400", "t3 -", "t3 -", "t3 k6=600"}
	line(reflect.DeepEqual(steps[:4], want[:4]), "八步1-4: %v", steps[:4])
	line(reflect.DeepEqual(steps[4:], want[4:]), "八步5-8: %v", steps[4:])
	B.Merge(A)
	tb, _ := B.Tomb("o")
	vb := B.View()
	line(tb == 3 && len(vb["o"]) == 1 && vb["o"]["k6"] == 600, "墓碑传播: B 反向合并后 tomb=%d 视图=%v", tb, vb["o"])

	X, Y := api.New(), api.New()
	apply(X, put("o", "k", 100, 5, "A"))
	apply(Y, put("o", "k", 999, 2, "B"))
	X.Merge(Y)
	Y.Merge(X)
	P, Q := api.New(), api.New()
	apply(P, put("o", "t", 1, 5, "A"))
	apply(Q, put("o", "t", 2, 5, "B"))
	P.Merge(Q)
	line(X.View()["o"]["k"] == 100 && Y.View()["o"]["k"] == 100 && P.View()["o"]["t"] == 2,
		"键级LWW收敛: ts5>2 双向均取100; 平局 rep B>A 取2")

	Z := api.New()
	apply(Z, put("o", "keep", 7, 1, "A"))
	before := Z.View()
	e1 := Z.Apply(api.Op{Kind: api.Put, Inner: "x", Value: 1, TS: 1, Rep: "A"})
	e2 := Z.Apply(put("o", "", 1, 1, "A"))
	e3 := Z.Apply(put("o", "x", 1, 0, "A"))
	distinct := e1 == api.ErrEmptyOuterKey && e2 == api.ErrEmptyInnerKey && e3 == api.ErrNonPositiveTS &&
		e1 != e2 && e2 != e3 && e1 != e3
	line(distinct, "三类可判定错误哨兵互不相同: %v/%v/%v", e1, e2, e3)
	unchanged := reflect.DeepEqual(Z.View()["o"], before["o"])
	contErr := Z.Apply(put("o", "after", 9, 2, "A"))
	line(unchanged && contErr == nil,
		"被拒后状态不变且可继续: keep 仍在=%v, 后续写入 err=%v", unchanged, contErr)

	constant := true
	for _, m := range []int{100, 1000, 10000} {
		om := omap.New()
		for j := 0; j < m; j++ {
			if err := om.Apply(put(fmt.Sprintf("outer%05d", j), "i", j, 1, "R")); err != nil {
				panic(err)
			}
		}
		if err := om.Apply(put("outer00000", "i", 1, 2, "R")); err != nil || !om.LastApplyConstantTime() {
			constant = false
		}
	}
	line(constant, "大m(100/1000/10000)下检查外层键数恒为常数(哈希定位)")

	F := api.New()
	for j := 0; j < 200; j++ {
		apply(F, put("o", fmt.Sprintf("i%03d", j), j, int64(j+1), "R"))
	}
	base := F.View()
	var wg sync.WaitGroup
	same := true
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < 50; r++ {
				if !reflect.DeepEqual(F.View(), base) {
					same = false
				}
			}
		}()
	}
	wg.Wait()
	line(same, "并发只读: 32 goroutine x 50 次 View 逐字段相同")
	line(api.New().SelfCheck() == nil, "SelfCheck: %v", api.New().SelfCheck())

	if failed {
		os.Exit(1)
	}
}
