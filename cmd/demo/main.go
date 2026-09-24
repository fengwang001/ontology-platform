// Command demo 校验 LSN 空洞检测与压缩重编号实现，逐条打印 OK/FAIL。
// 不读参数、不联网；退出码非 0 表示存在失败判定。
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/cmp"
	"ontology/lsn"
)

var failed bool

func check(name string, cond bool, detail ...any) {
	if cond {
		if len(detail) > 0 {
			fmt.Println("OK", name, fmt.Sprint(detail...))
		} else {
			fmt.Println("OK", name)
		}
	} else {
		failed = true
		fmt.Println("FAIL", name, fmt.Sprint(detail...))
	}
}

func main() {
	// 1+3. 第三节八步：每步空洞、第 6 步重复被拒、FindNew/FindOld 与互逆。
	e := api.New()
	seq := []int64{10, 13, 15, 12, 17, 10, 20, 18}
	wantHoles := [][]int64{{}, {11, 12}, {11, 12, 14}, {11, 14}, {11, 14, 16}, {11, 14, 16}, {11, 14, 16, 18, 19}, {11, 14, 16, 19}}
	holesOK, dupOK, inverse, gotHoles := true, false, true, [][]int64{}
	for i, v := range seq {
		err := e.Append(v)
		dupOK = dupOK || (i == 5 && errors.Is(err, lsn.ErrDuplicate))
		gotHoles = append(gotHoles, e.Holes())
		if !reflect.DeepEqual(gotHoles[i], wantHoles[i]) {
			holesOK = false
		}
	}
	n15, e15 := e.FindNew(15)
	o0, e0 := e.FindOld(0)
	if e15 != nil || n15 != 3 || e0 != nil || o0 != 10 {
		inverse = false
	}
	for k := 0; k < e.Count(); k++ {
		old, _ := e.FindOld(int64(k))
		back, err := e.FindNew(old)
		if err != nil || back != int64(k) {
			inverse = false
		}
	}
	check("8-step holes (per-step list)", holesOK, gotHoles)
	check("step6 duplicate rejected", dupOK)
	check("FindNew(15)=3, FindOld(0)=10, inverse", inverse)

	// 4. 四类可判定错误互不相同，被拒后状态不变、仍可继续使用。
	bN, bH := e.Count(), e.Holes()
	errs := []error{
		e.Append(-1), e.Append(10),
		func() error { _, err := e.FindNew(999); return err }(),
		func() error { _, err := e.FindOld(-1); return err }(),
	}
	wants := []error{lsn.ErrNegative, lsn.ErrDuplicate, cmp.ErrUnknownLSN, cmp.ErrNewIndexOutOfRange}
	errsOK := true
	for i := range errs {
		if !errors.Is(errs[i], wants[i]) {
			errsOK = false
		}
	}
	for i := 0; i < len(wants); i++ {
		for j := i + 1; j < len(wants); j++ {
			if errors.Is(wants[i], wants[j]) {
				errsOK = false
			}
		}
	}
	if k, err := e.FindNew(13); err != nil || k != 2 || e.Count() != bN || !reflect.DeepEqual(e.Holes(), bH) {
		errsOK = false // 八步集合 {10,12,13,...} 中 13 的正确名次为 2
	}
	check("4 distinct sentinel errors, no trace, still usable", errsOK)

	// 5. 大 m 下二分定位（比较个数上界 <⌈log2m⌉+2 由 lsn 同包内部测试读 probe 钉住）。
	largeOK := true
	for _, m := range []int{100, 1000, 10000} {
		t := api.New()
		r := rand.New(rand.NewSource(int64(m)))
		perm := r.Perm(m)
		for _, p := range perm {
			if err := t.Append(int64(p) * 7); err != nil {
				largeOK = false
			}
		}
		if nk, err := t.FindNew(int64(perm[0]) * 7); err != nil || nk != int64(perm[0]) {
			largeOK = false
		}
	}
	check("large-m locate m=100..10000 (probe bound: lsn internal test)", largeOK)

	// 6. N 个 goroutine 并发只读，Holes 与 FindNew 逐值相同（无 sleep）。
	r := rand.New(rand.NewSource(7))
	pool := r.Perm(5000)[:500]
	c := api.New()
	for _, p := range pool {
		_ = c.Append(int64(p))
	}
	refH, refN := c.Holes(), make([]int64, len(pool))
	for i, p := range pool {
		refN[i], _ = c.FindNew(int64(p))
	}
	const N = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	concOK := true
	start := make(chan struct{})
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ok := reflect.DeepEqual(c.Holes(), refH)
			for i, p := range pool {
				if nk, err := c.FindNew(int64(p)); err != nil || nk != refN[i] {
					ok = false
				}
			}
			if !ok {
				mu.Lock()
				concOK = false
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	check("concurrent read-only consistent", concOK)

	// 7. 对外自检：四条不变量内置序列核验。
	check("SelfCheck", e.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
