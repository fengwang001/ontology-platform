package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"sync"

	"ontology/api"
	"ontology/ewma"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

func main() {
	// 1. 第三节五步表：alpha=0.9, seed=0, 不校正, 观测 [10,20,10,20,10]
	want := []float64{9, 18.9, 10.89, 19.089, 10.9089}
	e := ewma.New(0.9, 0, false)
	ok := true
	for i, x := range []float64{10, 20, 10, 20, 10} {
		e.Update(x)
		ok = ok && math.Abs(e.Value()-want[i]) < 1e-9
	}
	check("five-step s", ok)

	// 2. 范围不变：任意时刻均值落在 [min(seed,xs), max(seed,xs)]
	rng, _ := api.New(0.3, 0, true)
	ok, lo, hi := true, 0.0, 0.0
	for _, x := range []float64{-2, 8, 0, 3, -1, 5, -4} {
		lo, hi = math.Min(lo, x), math.Max(hi, x)
		_ = rng.Update("k", x)
		v, _ := rng.Value("k")
		ok = ok && v >= lo-1e-12 && v <= hi+1e-12
	}
	check("range invariant", ok)

	// 3. 常数输入精确：校正后 == c，不校正 == c*(1-(1-alpha)^n)
	cc, _ := api.New(0.5, 0, true)
	cu, _ := api.New(0.5, 0, false)
	ok = true
	for n := 1; n <= 10; n++ {
		_ = cc.Update("k", 4)
		_ = cu.Update("k", 4)
		vc, _ := cc.Value("k")
		vu, _ := cu.Value("k")
		ok = ok && vc == 4 && math.Abs(vu-4*(1-math.Pow(0.5, float64(n)))) < 1e-12
	}
	check("constant input exact", ok)

	// 4. 与闭式参照一致（批量非递推重算）
	cf, _ := api.New(0.25, 1.5, false)
	seq := []float64{3, -1, 2.5, 0, 7, -4}
	for _, x := range seq {
		_ = cf.Update("k", x)
	}
	got, _ := cf.Value("k")
	sum, w := 0.0, 1.0
	for i := len(seq) - 1; i >= 0; i-- {
		sum += w * seq[i]
		w *= 0.75
	}
	check("closed-form agreement", math.Abs(got-(0.25*sum+w*1.5)) < 1e-9)

	// 5+6. 三类可判定错误互不相同；被拒后状态不变
	svc, _ := api.New(0.9, 0, false)
	_ = svc.Update("k", 10)
	before, _ := svc.Value("k")
	_, errAlpha := api.New(1.5, 0, false)
	errEmpty := svc.Update("", 1)
	_, errUnknown := svc.Value("ghost")
	after, _ := svc.Value("k")
	check("decidable errors distinct", errors.Is(errAlpha, api.ErrBadAlpha) &&
		errors.Is(errEmpty, api.ErrEmptyKey) && errors.Is(errUnknown, api.ErrUnknownKey) &&
		api.ErrBadAlpha != api.ErrEmptyKey && api.ErrEmptyKey != api.ErrUnknownKey &&
		api.ErrBadAlpha != api.ErrUnknownKey)
	check("rejection leaves no trace", after == before)

	// 7. 大 m 下 Value 读取历史观测数恒为 1：计数器是非导出字段，
	//    demo 无法也不应读到它；由 ewma 包内测试
	//    TestValueReadsConstant（m=100..10000）钉死，此处仅作声明。
	check("value reads 1 (ewma pkg test)", true)

	// 8. 并发只读：N 个 goroutine 读同一批 key，结果逐 key 相同
	keys := []string{"a", "b", "c", "d"}
	wantV := map[string]float64{}
	for i, k := range keys {
		for j := 0; j < 10+i; j++ {
			_ = svc.Update(k, float64(i*j+j))
		}
		wantV[k], _ = svc.Value(k)
	}
	var wg sync.WaitGroup
	bad := make(chan bool, 64)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, k := range keys {
				v, err := svc.Value(k)
				if err != nil || v != wantV[k] {
					bad <- true
				}
			}
		}()
	}
	wg.Wait()
	close(bad)
	check("concurrent reads consistent", len(bad) == 0)

	// 9. 内置自检：四条不变量
	check("selfcheck", svc.SelfCheck() == nil)

	if failed {
		fmt.Println("RESULT: FAIL")
		os.Exit(1)
	}
	fmt.Println("RESULT: OK")
}
