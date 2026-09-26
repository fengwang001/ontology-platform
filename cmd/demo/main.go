// 演示程序：逐条打印 OK/FAIL，任一失败则以非零码退出。
package main

import (
	"fmt"
	"os"
	"sync"

	"ontology/api"
)

var failed bool

func check(name string, ok bool, detail string) {
	if ok {
		fmt.Printf("OK   %s %s\n", name, detail)
	} else {
		fmt.Printf("FAIL %s %s\n", name, detail)
		failed = true
	}
}

func eq(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func main() {
	// 1. 第三节四步之后的计数器数组（m=8, k=3）
	f, _ := api.New(8, 3)
	want := [][]int64{
		{0, 1, 0, 1, 0, 0, 1, 0}, // Add(3)
		{0, 1, 1, 1, 0, 1, 1, 1}, // Add(5)
		{0, 1, 1, 1, 0, 2, 2, 2}, // Add(7)
		{0, 1, 0, 1, 0, 1, 2, 1}, // Remove(5)
	}
	ok := true
	f.Add(3)
	ok = ok && eq(f.Snapshot(), want[0])
	f.Add(5)
	ok = ok && eq(f.Snapshot(), want[1])
	f.Add(7)
	ok = ok && eq(f.Snapshot(), want[2])
	f.Remove(5)
	ok = ok && eq(f.Snapshot(), want[3])
	check("四步计数器", ok, fmt.Sprint(f.Snapshot()))

	// 2. Query(3)/Query(5)/Query(7)
	q3, _ := f.Query(3)
	q5, _ := f.Query(5)
	q7, _ := f.Query(7)
	check("Query(3/5/7)", q3 == 1 && q5 == 0 && q7 == 1, fmt.Sprintf("=%d/%d/%d", q3, q5, q7))

	// 3. 无假阴性；4. 精确移除；5. 与朴素重放一致；6. 失败不留痕（SelfCheck 全覆盖）
	check("SelfCheck(不变量1-4)", f.SelfCheck() == nil, "")

	// 7. 三类可判定错误互不相同
	_, e1 := api.New(0, 3)
	e2 := f.Add(-1)
	e3 := f.Remove(5) // 第二次删除：c[2]=0
	distinct := e1 == api.ErrInvalidParam && e2 == api.ErrInvalidKey && e3 == api.ErrNotPresent &&
		e1 != e2 && e2 != e3 && e1 != e3
	check("三类哨兵错误", distinct, fmt.Sprintf("%v | %v | %v", e1, e2, e3))

	// 8. 被拒后状态不变
	check("被拒后状态不变", eq(f.Snapshot(), want[3]), "")

	// 9. 大 m 下 Query 访问计数器数恒等于 k
	ok = true
	for _, m := range []int{100, 1000, 10000} {
		g, _ := api.New(m, 5)
		for x := int64(0); x < int64(m); x++ {
			g.Add(x)
		}
		g.Query(42)
		ok = ok && g.QueryLocalityOK()
	}
	check("Query 访问数恒为 k", ok, "m=100/1000/10000")

	// 10. 并发查询结果逐 key 相同
	g, _ := api.New(256, 4)
	for x := int64(0); x < 128; x++ {
		g.Add(x)
	}
	const n = 8
	res := make([][]int64, n)
	var wg sync.WaitGroup
	for gID := 0; gID < n; gID++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			res[id] = make([]int64, 128)
			for x := int64(0); x < 128; x++ {
				res[id][x], _ = g.Query(x)
			}
		}(gID)
	}
	wg.Wait()
	ok = true
	for i := 1; i < n; i++ {
		ok = ok && eq(res[0], res[i])
	}
	check("并发查询一致", ok, fmt.Sprintf("%d goroutines x 128 keys", n))

	if failed {
		os.Exit(1)
	}
}
