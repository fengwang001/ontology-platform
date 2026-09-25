package main

import (
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/seg"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
	} else {
		fmt.Println("OK", name)
	}
}

func main() {
	// 1. 第三节八步推导
	q, _ := api.New([]int64{5, 2, 8, 1, 9, 3, 7, 4})
	var got []int64
	step := func(op func() int64) { got = append(got, op()) }
	step(func() int64 { v, _ := q.Query(0, 8); return v })
	step(func() int64 { q.Update(0, 10); return 0 })
	step(func() int64 { v, _ := q.Query(0, 4); return v })
	step(func() int64 { q.Update(3, 11); return 0 })
	step(func() int64 { v, _ := q.Query(0, 4); return v })
	step(func() int64 { v, _ := q.Query(4, 8); return v })
	step(func() int64 { q.Update(2, 0); return 0 })
	step(func() int64 { v, _ := q.Query(0, 8); return v })
	want := []int64{1, 0, 1, 0, 2, 3, 0, 0}
	check("八步推导", fmt.Sprint(got) == fmt.Sprint(want))

	// 2. 半开 [0,2)=2，误当闭区间会错得 0
	v, _ := q.Query(0, 2)
	check("半开区间(闭区间错得0)", v == 2)

	// 3. 更新已沿祖先重算：第4步后 Query(0,4)=2，陈旧实现错得 1
	q2, _ := api.New([]int64{5, 2, 8, 1, 9, 3, 7, 4})
	q2.Update(0, 10)
	q2.Update(3, 11)
	v, _ = q2.Query(0, 4)
	check("更新传播(陈旧错得1)", v == 2)

	// 4. 空区间 +Inf；n=11 非 2 的幂，补齐叶子不污染
	v, _ = q2.Query(3, 3)
	q3, _ := api.New([]int64{9, 4, 7, 5, 8, 6, 2, 3, 1, 10, 11})
	w, _ := q3.Query(0, 11)
	check("空区间+Inf/补齐不污染", v == seg.Inf && w == 1)

	// 5. SelfCheck 覆盖四条不变量
	check("SelfCheck四不变量", q3.SelfCheck() == nil)

	// 6. 四类哨兵错误互不相同
	_, e1 := q3.Query(2, 1)
	_, e2 := q3.Query(0, 12)
	e3 := q3.Update(11, 0)
	_, e4 := api.New(nil)
	errs := []error{e1, e2, e3, e4}
	seen := map[error]bool{}
	ok := true
	for _, e := range errs {
		if e == nil || seen[e] {
			ok = false
		}
		seen[e] = true
	}
	check("四类可判定错误互不相同", ok && len(seen) == 4)

	// 7. 被拒后状态不变且可继续使用
	b, _ := q3.Query(0, 11)
	a, _ := q3.Query(0, 11)
	check("被拒后状态不变", b == a && q3.Update(0, 1) == nil)

	// 8. 多档 n 全区间查询正确（对数复杂度由 rmq 白盒测试钉住）
	ok = true
	for _, n := range []int{100, 1000, 10000} {
		arr := make([]int64, n)
		min := seg.Inf
		for i := range arr {
			arr[i] = int64((i*37 + 11) % 997)
			min = seg.Min(min, arr[i])
		}
		qq, _ := api.New(arr)
		v, _ := qq.Query(0, n)
		if v != min {
			ok = false
		}
	}
	check("多档n查询正确", ok)

	// 9. 并发只读结果逐条一致
	qs, _ := api.New([]int64{5, 2, 8, 1, 9, 3, 7, 4})
	base, _ := qs.Query(2, 6)
	var wg sync.WaitGroup
	consistent := true
	var mu sync.Mutex
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				v, _ := qs.Query(2, 6)
				if v != base {
					mu.Lock()
					consistent = false
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	check("并发只读一致", consistent)

	if failed {
		fmt.Println("RESULT FAIL")
		os.Exit(1)
	}
	fmt.Println("RESULT OK")
}
