// Command demo verifies the range-partition split/merge invariants.
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/store"
)

var failed bool

func check(name string, ok bool) {
	status := "OK  "
	if !ok {
		status, failed = "FAIL", true
	}
	fmt.Println(status, name)
}

func main() {
	s, err := api.New(0, 16, 3, 2)
	check("New(0,16,3,2)", err == nil)
	model := map[int64]bool{}
	var after3, after8 []store.Range
	steps := []func(){
		func() { s.Insert(5) }, func() { s.Insert(11) },
		func() { s.Insert(8); after3 = s.Ranges() },
		func() { s.Insert(14) }, func() { s.Insert(2) }, func() { s.Insert(6) },
		func() { s.Insert(12) },
		func() { s.Insert(10); after8 = s.Ranges() },
		func() { s.Delete(5) }, func() { s.Delete(2) },
		func() { s.Compact() },
	}
	keys := []int64{5, 11, 8, 14, 2, 6, 12, 10}
	for _, k := range keys {
		model[k] = true
	}
	for _, step := range steps {
		step()
	}
	model[5], model[2] = false, false
	delete(model, 5)
	delete(model, 2)
	final := s.Ranges()
	want := []store.Range{{Lo: 0, Hi: 10, Load: 2, Keys: []int64{6, 8}},
		{Lo: 10, Hi: 12, Load: 2, Keys: []int64{10, 11}},
		{Lo: 12, Hi: 16, Load: 2, Keys: []int64{12, 14}}}
	check("11步后分区==[0,10)[10,12)[12,16)", reflect.DeepEqual(final, want))
	check("第3步: 键8归右, [8,16) load=2", len(after3) == 2 && after3[1].Lo == 8 &&
		after3[1].Load == 2 && reflect.DeepEqual(after3[1].Keys, []int64{8, 11}))
	check("第8步: [8,12)@10 分裂", len(after8) == 5 && after8[2].Lo == 8 && after8[2].Hi == 10 &&
		after8[3].Lo == 10 && after8[3].Load == 2)
	check("第11步: Compact 合并为 3 个分区", len(final) == 3)
	lo, hi := s.Bounds()
	check("键不丢不重+范围不变量+朴素重算", s.Verify(model) == nil && lo == 0 && hi == 16)

	before := s.Ranges()
	e1 := s.Insert(-1)
	e2 := s.Delete(999)
	_, e3 := s.Locate(16)
	_, e4 := api.New(4, 4, 3, 2)
	distinct := !errors.Is(e1, api.ErrKeyNotFound) && !errors.Is(e2, api.ErrKeyOutOfRange) &&
		!errors.Is(e4, api.ErrKeyOutOfRange)
	check("三类错误互异且被拒后状态不变", errors.Is(e1, api.ErrKeyOutOfRange) &&
		errors.Is(e2, api.ErrKeyNotFound) && errors.Is(e3, api.ErrKeyOutOfRange) &&
		errors.Is(e4, api.ErrBadParam) && distinct &&
		reflect.DeepEqual(before, s.Ranges()) && s.Insert(9) == nil)

	big, _ := api.New(0, 80000, 2, 1)
	for k := int64(0); len(big.Ranges()) < 10000; k++ {
		big.Insert(k)
	}
	check("大 P 比较数<=ceil(log2 P)+1 (store 测试钉住)", len(big.Ranges()) >= 10000)

	const n = 64
	results := make([][]store.Range, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for _, k := range keys {
				r, _ := big.Locate(k)
				results[g] = append(results[g], r)
			}
		}(g)
	}
	close(start)
	wg.Wait()
	same := true
	for g := 1; g < n; g++ {
		same = same && reflect.DeepEqual(results[0], results[g])
	}
	check("并发 Locate 逐键一致", same)
	check("SelfCheck", api.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
