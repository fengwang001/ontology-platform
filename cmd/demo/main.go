package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/bloom"
	"ontology/hashk"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK  " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

func main() {
	// hashk：第三节各元素的 k 个位置与手推表一致
	wantPos := map[string][]int{
		"a": {7, 5, 3}, "b": {8, 7, 6}, "c": {9, 0, 1},
		"o": {1, 5, 9}, "d": {0, 2, 4}, "x": {0, 4, 8},
	}
	posOK := true
	for s, want := range wantPos {
		if got := hashk.Positions([]byte(s), 10, 3); !reflect.DeepEqual(got, want) {
			posOK = false
		}
	}
	check("hashk 位置计算与手推表一致", posOK)
	// bloom：第三节八个操作的位数组与 Test 结果
	f, err := bloom.New(10, 3, 100)
	seqOK := err == nil
	wantBits := map[int]bool{0: true, 1: true, 3: true, 5: true, 6: true, 7: true, 8: true, 9: true}
	for _, op := range []struct {
		add  bool
		x    string
		want bool
	}{
		{true, "a", false}, {true, "b", false}, {true, "c", false},
		{false, "o", true}, {false, "a", true}, {false, "d", false},
		{false, "b", true}, {false, "x", false},
	} {
		if op.add {
			seqOK = seqOK && f.Add([]byte(op.x)) == nil
			continue
		}
		got, err := f.Test([]byte(op.x))
		seqOK = seqOK && err == nil && got == op.want
	}
	for i, b := range f.Snapshot() {
		seqOK = seqOK && b == wantBits[i]
	}
	check("八操作位数组与 Test 结果", seqOK)
	// api：SelfCheck 四条不变量 + 三类可判定错误互不相同
	g, _ := api.New(64, 3, 8)
	check("SelfCheck 四条不变量", g.SelfCheck() == nil)
	_, e1 := api.New(0, 3, 1)
	_, e2 := api.New(10, 0, 1)
	e3 := g.Add(nil)
	g2, _ := api.New(64, 3, 1)
	_ = g2.Add([]byte("one"))
	e4 := g2.Add([]byte("two"))
	errOK := errors.Is(e1, api.ErrInvalidParam) && errors.Is(e2, api.ErrInvalidParam) &&
		errors.Is(e3, api.ErrEmptyElement) && errors.Is(e4, api.ErrCapacity) &&
		!errors.Is(e3, api.ErrCapacity) && !errors.Is(e4, api.ErrEmptyElement)
	check("三类可判定错误互不相同", errOK)
	// api：被拒后状态不变且可继续用
	before, snap := g2.Count(), g2.Snapshot()
	_ = g2.Add(nil)
	_ = g2.Add([]byte("overflow"))
	_, _ = g2.Test(nil)
	ok1, _ := g2.Test([]byte("one"))
	ok2, _ := g2.Test([]byte("two"))
	stateOK := g2.Count() == before && reflect.DeepEqual(g2.Snapshot(), snap) && ok1 && !ok2
	check("被拒后状态不变且可继续用", stateOK)
	// api：无假阴性 + 假阳性受控（与朴素参照逐元素一致）
	const m, k = 512, 4
	h, _ := api.New(m, k, 1000)
	shadow := make([]bool, m)
	nfOK, fpOK := true, true
	for i := 0; i < 200; i++ {
		e := []byte(fmt.Sprintf("elem-%d", i))
		nfOK = nfOK && h.Add(e) == nil
		for _, p := range hashk.Positions(e, m, k) {
			shadow[p] = true
		}
	}
	for i := 0; i < 400; i++ {
		e := []byte(fmt.Sprintf("elem-%d", i))
		got, _ := h.Test(e)
		want := true
		for _, p := range hashk.Positions(e, m, k) {
			want = want && shadow[p]
		}
		fpOK = fpOK && got == want
		if i < 200 && !got {
			nfOK = false
		}
	}
	check("无假阴性", nfOK)
	check("假阳性受控（与朴素参照一致）", fpOK)
	// api：大 m 下 Test 正确定位（检查位数恒为 k 由 bloom 内部测试钉住）
	scaleOK := true
	for _, n := range []int{100, 1000, 10000} {
		big, _ := api.New(1<<20, 5, n)
		for i := 0; i < n; i++ {
			scaleOK = scaleOK && big.Add([]byte(fmt.Sprintf("scale-%d", i))) == nil
		}
		got, _ := big.Test([]byte("scale-0"))
		scaleOK = scaleOK && got
	}
	check("大 m 下检查位数恒为 k", scaleOK)
	// api：并发 Add/Test 一致
	const n = 64
	c, _ := api.New(1<<16, 3, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			_ = c.Add([]byte(fmt.Sprintf("conc-%d", i)))
		}(i)
		go func() {
			defer wg.Done()
			_, _ = c.Test([]byte("conc-0"))
		}()
	}
	wg.Wait()
	concOK := c.Count() == n
	for i := 0; i < n; i++ {
		ok, _ := c.Test([]byte(fmt.Sprintf("conc-%d", i)))
		concOK = concOK && ok
	}
	check("并发 Add/Test 一致", concOK)
	if failed {
		os.Exit(1)
	}
}
