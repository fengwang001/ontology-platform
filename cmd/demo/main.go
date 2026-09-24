package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/cbf"
	"ontology/hash"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK " + name)
	} else {
		failed = true
		fmt.Println("FAIL " + name)
	}
}

func counters(p hash.Params, ops ...int64) []uint8 {
	c := make([]uint8, p.M())
	for _, x := range ops { // 仅模拟成功插入；失败操作由调用方排除
		for _, q := range p.Positions(x) {
			c[q]++
		}
	}
	return c
}

func main() {
	// 第三节七步：计数器值由 hash 双重散列本地重算打印；
	// 真实过滤器内部计数器与这些行逐格相等，由 f.SelfCheck() 断言。
	p7, _ := hash.New(7, 3)
	r1 := counters(p7, 1)
	r2 := counters(p7, 1, 4)
	r3 := counters(p7, 1, 4, 3)
	f, err := api.New(7, 3, 2)
	e4 := f.Insert(1)
	e1, e2 := f.Insert(4), f.Insert(3)
	eOvf := f.Insert(3) // 第二次 Insert(3)：位置 3 已 = 2
	check(fmt.Sprintf("steps1-4 %v %v %v %v; step4", r1, r2, r3, r3),
		err == nil && e1 == nil && e2 == nil && e4 == nil && errors.Is(eOvf, cbf.ErrOverflow))
	c5 := f.Contains(2) // 假阳性
	eDel := f.Delete(2) // 2 从未插入
	c7 := f.Contains(1)
	check(fmt.Sprintf("steps5-7 %v %v %v; s5=%v(FP) s6=%v s7=%v", r3, r3, r3, c5, eDel, c7),
		c5 && errors.Is(eDel, cbf.ErrDeleteUninserted) && c7)

	check("SelfCheck: four invariants on built-in sequences", f.SelfCheck() == nil)

	// 无假阴性：含重复插入与删除，仍在册（净次数>0）的键必 true。
	fn, _ := api.New(101, 5, 255)
	live := map[int64]bool{}
	for i := int64(0); i < 21; i++ {
		fn.Insert(i)
		live[i] = true
	}
	for _, x := range []int64{2, 2, 7} {
		fn.Insert(x)
	}
	for _, x := range []int64{2, 7, 13, 13} {
		fn.Delete(x) // 2、7 仍各剩 1 次；13 删净
		live[13] = false
	}
	noFN := true
	for x, on := range live {
		if on && !fn.Contains(x) { // 仍在册的键绝不能 false
			noFN = false
		}
	}
	_ = fn.Contains(13) // 13 已删净；是否命中是允许的假阳性，不影响无假阴性结论
	check("no false negatives under repeated insert/delete", noFN && fn.Insert(13) == nil)

	// 被拒 Insert 不留痕：两次成功插入后第三次溢出被拒，再删两次必删净，
	// 且实例可继续使用。若被拒操作偷增了计数器或键计数，Contains 仍会是 true。
	tr, _ := api.New(7, 3, 2)
	tr.Insert(1)
	tr.Insert(1)
	ovf3 := tr.Insert(1)
	d1 := tr.Delete(1)
	d2 := tr.Delete(1)
	check("overflow rejected leaves no trace; state reusable",
		errors.Is(ovf3, cbf.ErrOverflow) && d1 == nil && d2 == nil &&
			!tr.Contains(1) && tr.Insert(1) == nil)

	// 三类可判定错误互不相同；非法参数含 m 非素数与 maxCount=0。
	_, eParam := api.New(6, 3, 2)
	_, eZero := api.New(7, 3, 0)
	_, eBadK := api.New(3, 5, 2)
	check("three distinct sentinel errors",
		errors.Is(eParam, hash.ErrInvalidParameters) && errors.Is(eZero, hash.ErrInvalidParameters) &&
			errors.Is(eBadK, hash.ErrInvalidParameters) &&
			!errors.Is(cbf.ErrOverflow, cbf.ErrDeleteUninserted) &&
			!errors.Is(cbf.ErrOverflow, hash.ErrInvalidParameters))

	check("visited counters == k for m in {101..9973}, independent of m (value hidden)",
		f.CheckAccessComplexity() == nil)

	// 并发 Contains：先填满，N 个 goroutine 并发只读，结果逐键一致。
	fc, _ := api.New(1009, 6, 255)
	keys := make([]int64, 80)
	for i := range keys {
		keys[i] = int64(i)
		if i < 50 {
			fc.Insert(keys[i])
		}
	}
	want := make([]bool, len(keys))
	for i, x := range keys {
		want[i] = fc.Contains(x)
	}
	const N = 16
	res := make([][]bool, N)
	var wg sync.WaitGroup
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			r := make([]bool, len(keys))
			for i, x := range keys {
				r[i] = fc.Contains(x)
			}
			res[g] = r
		}(g)
	}
	wg.Wait()
	same := true
	for g := 0; g < N && same; g++ {
		for i := range want {
			if res[g][i] != want[i] {
				same = false
			}
		}
	}
	check("concurrent Contains: 16 goroutines agree per key (incl. false positives)", same)

	if failed {
		os.Exit(1)
	}
}
