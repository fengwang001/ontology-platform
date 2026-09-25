// Command demo 演示列式物化视图：逐条打印 OK/FAIL，退出码 0 表示全部通过。
// 输出严格控制在 10 行以内：前 8 行是第三节八步，后 2 行汇总其余判定。
package main

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"ontology/api"
)

type want struct {
	a, b, c []int64
	l       []bool
	s       []int
}

var fail bool

func ok(cond bool, msg string) {
	if cond {
		fmt.Println("OK " + msg)
	} else {
		fail = true
		fmt.Println("FAIL " + msg)
	}
}

func state(x *api.API) want {
	a, b, c, l, s := x.Snapshot()
	return want{a, b, c, l, s}
}

func main() {
	x := api.New()
	wants := []want{
		{[]int64{10}, []int64{20}, []int64{30}, []bool{true}, []int{0}},
		{[]int64{10, 1}, []int64{20, 2}, []int64{30, 3}, []bool{true, true}, []int{0, 1}},
		{[]int64{10, 1, 100}, []int64{20, 2, 200}, []int64{30, 3, 300}, []bool{true, true, true}, []int{0, 1, 2}},
		{[]int64{10, 1, 100}, []int64{20, 2, 200}, []int64{30, 3, 300}, []bool{false, true, true}, []int{-1, 1, 2}},
		{[]int64{10, 1, 100, 7}, []int64{20, 2, 200, 8}, []int64{30, 3, 300, 9}, []bool{false, true, true, true}, []int{-1, 1, 2, 3}},
		{[]int64{1, 100, 7}, []int64{2, 200, 8}, []int64{3, 300, 9}, []bool{true, true, true}, []int{-1, 0, 1, 2}},
		{[]int64{1, 100, 7}, []int64{2, 200, 8}, []int64{999, 300, 9}, []bool{true, true, true}, []int{-1, 0, 1, 2}},
		{[]int64{1, 100, 7}, []int64{2, 200, 8}, []int64{999, 300, 9}, []bool{true, true, true}, []int{-1, 0, 1, 2}},
	}
	for i := range 8 {
		extra := ""
		switch i {
		case 0, 1, 2:
			x.Insert([3][3]int64{{10, 20, 30}, {1, 2, 3}, {100, 200, 300}}[i][0],
				[3][3]int64{{10, 20, 30}, {1, 2, 3}, {100, 200, 300}}[i][1],
				[3][3]int64{{10, 20, 30}, {1, 2, 3}, {100, 200, 300}}[i][2])
		case 3:
			x.Delete(0)
			_, _, _, ge := x.Get(0)           // (丙)：已删报 ErrDeleted
			pb, _ := x.Project([]string{"B"}) // (乙)：[2 200]，不多出 20
			extra = fmt.Sprintf(" Get0=%v ProjectB=%v", ge, pb["B"])
		case 4:
			x.Insert(7, 8, 9)
		case 5:
			x.Compact()
		case 6:
			x.Update(1, "C", 999)
		case 7: // (甲)：Get(rowID2)=(1,2,999)
			a, b, c, e := x.Get(1)
			extra = fmt.Sprintf(" Get(rowID2)=(%d,%d,%d),%v", a, b, c, e)
		}
		st := state(x)
		match := reflect.DeepEqual(st, wants[i])
		ok(match, fmt.Sprintf("step%d a=%v b=%v c=%v alive=%v slotOf=%v%s",
			i+1, st.a, st.b, st.c, st.l, st.s, extra))
	}

	// 第 9 行：Compact 后绑定不变已在 step6-8；三类哨兵互不相同、拒绝不留痕、可继续使用。
	y := api.New()
	id := y.Insert(1, 2, 3)
	y.Delete(id)
	a0, b0, c0, l0, s0 := y.Snapshot()
	_, _, _, eDel := y.Get(id)
	_, _, _, eMiss := y.Get(42)
	_, eEmpty := y.Project(nil)
	a1, b1, c1, l1, s1 := y.Snapshot()
	distinct := errors.Is(eDel, api.ErrDeleted) && errors.Is(eMiss, api.ErrNoSuchRow) &&
		errors.Is(eEmpty, api.ErrNoColumns) && !errors.Is(eDel, eMiss) && !errors.Is(eDel, eEmpty)
	untraced := reflect.DeepEqual([]any{a0, b0, c0, l0, s0}, []any{a1, b1, c1, l1, s1})
	ok(distinct && untraced && y.Insert(4, 5, 6) == 1 &&
		func() bool { a, b, c, e := y.Get(1); return e == nil && a == 4 && b == 5 && c == 6 }(),
		"3 distinct sentinel errors; rejected ops leave no trace; instance still usable")

	// 第 10 行：大 m 下 Get 检查槽数不随 m 线性增长；并发只读结果逐字段一致。
	z := api.New()
	for i := range 400 {
		z.Insert(int64(i), 0, 0)
		if i%3 == 0 {
			z.Delete(i)
		}
	}
	z.Compact()
	const n = 8
	var wg sync.WaitGroup
	res := make([][][3]int64, n)
	for g := range n {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for id := range 400 {
				a, b, c, e := z.Get(id)
				if !errors.Is(e, api.ErrDeleted) {
					res[g] = append(res[g], [3]int64{a, b, c})
				}
			}
		}(g)
	}
	wg.Wait()
	same := true
	for g := 1; g < n; g++ {
		same = same && reflect.DeepEqual(res[0], res[g])
	}
	ok(api.New().LookupConstantTime([]int{100, 1000, 10000}) && same,
		"Get probe count is O(1) across m; 8 goroutines read identical results")

	if fail {
		panic("demo checks failed") // panic => 非零退出码；正常时退出码为 0
	}
}
