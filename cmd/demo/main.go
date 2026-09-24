package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/hash"
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
	// 1. hash：指纹范围与 alternate 对称性
	ok := true
	for x := int64(0); x < 700 && ok; x++ {
		f := hash.Fingerprint(x)
		if f < 1 || f > 7 || hash.Alternate(hash.Alternate(3, f), f) != 3 {
			ok = false
		}
	}
	check("hash fingerprint/alternate", ok)

	// 2. 第三节十步表：每步之后的桶状态 + 第 7、8、9 步判定
	f, _ := api.New(4, 2, 4)
	want := [][][]uint8{
		{{}, {2}, {}, {}},             // 1 Insert(1)
		{{}, {2, 6}, {}, {}},          // 2 Insert(5)
		{{3}, {2, 6}, {}, {}},         // 3 Insert(9) 入 i2=b0
		{{3}, {2, 6}, {3}, {}},        // 4 Insert(2)
		{{3}, {2, 6}, {3, 7}, {}},     // 5 Insert(6)
		{{3, 4}, {2, 6}, {3, 7}, {}},  // 6 Insert(10) 入 i2=b0
		{{3, 4}, {2, 6}, {1, 7}, {3}}, // 7 Insert(14) 踢 f=3: b2->b3，f=1 占空位
		{{3, 4}, {2, 6}, {1, 7}, {3}}, // 8 Lookup(2)=true
		{{4}, {2, 6}, {1, 7}, {3}},    // 9 Delete(9) 删 b0 的 3
		{{4}, {2, 6}, {1, 7}, {3}},    // 10 Lookup(2)=true
	}
	ok = true
	ops := []func() bool{
		func() bool { return f.Insert(1) == nil },
		func() bool { return f.Insert(5) == nil },
		func() bool { return f.Insert(9) == nil },
		func() bool { return f.Insert(2) == nil },
		func() bool { return f.Insert(6) == nil },
		func() bool { return f.Insert(10) == nil },
		func() bool { return f.Insert(14) == nil }, // 踢 f=3: b2->b3
		func() bool { return f.Lookup(2) },
		func() bool { return f.Delete(9) == nil },
		func() bool { return f.Lookup(2) },
	}
	for s, op := range ops {
		ok = ok && op() && reflect.DeepEqual(norm(f.Buckets()), norm(want[s]))
	}
	check("ten-step table + kick f=3 b2->b3 + Lookup(2)=true", ok)

	// 3. api.SelfCheck：无假阴性、指纹守恒、满回滚、四类哨兵错误
	check("api.SelfCheck (invariants 1-4)", api.SelfCheck() == nil)

	// 4. 四类错误互不相同
	errs := []error{api.ErrInvalidParams, api.ErrNegativeKey, api.ErrFull, api.ErrNotInserted}
	seen := map[string]bool{}
	ok = true
	for _, e := range errs {
		if seen[e.Error()] {
			ok = false
		}
		seen[e.Error()] = true
	}
	_, e1 := api.New(3, 1, 1)
	g, _ := api.New(2, 1, 1)
	_ = g.Insert(0)
	e2 := g.Insert(-1)
	_ = g.Insert(2)
	e3 := g.Insert(4) // 满
	e4 := g.Delete(99)
	ok = ok && errors.Is(e1, api.ErrInvalidParams) && errors.Is(e2, api.ErrNegativeKey) &&
		errors.Is(e3, api.ErrFull) && errors.Is(e4, api.ErrNotInserted) &&
		g.Lookup(0) && g.Lookup(2) // 被拒后状态不变仍可用
	check("four distinct sentinel errors + state intact", ok)

	// 5. 大桶数 Lookup 正确（检查桶数恒为 2 由 cuck 内部测试钉住，计数器不可导出）
	ok = true
	for _, nb := range []int{128, 1024, 8192} {
		h, _ := api.New(nb, 4, 8)
		for x := int64(0); x < 500; x++ {
			_ = h.Insert(x)
		}
		for x := int64(0); x < 500; x++ {
			if !h.Lookup(x) {
				ok = false
			}
		}
	}
	check("large numBuckets lookup (checked==2 proven in cuck test)", ok)

	// 6. 并发 Lookup 结果逐键一致
	c, _ := api.New(256, 4, 16)
	for x := int64(0); x < 600; x++ {
		_ = c.Insert(x)
	}
	wantRes := map[int64]bool{}
	for x := int64(0); x < 1200; x++ {
		wantRes[x] = c.Lookup(x)
	}
	var wg sync.WaitGroup
	bad := make(chan bool, 8)
	for gID := 0; gID < 8; gID++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for x := int64(0); x < 1200; x++ {
				if c.Lookup(x) != wantRes[x] {
					bad <- true
				}
			}
		}()
	}
	wg.Wait()
	close(bad)
	_, incon := <-bad
	check("concurrent Lookup consistent", !incon)

	if failed {
		os.Exit(1)
	}
}

func norm(b [][]uint8) [][]uint8 {
	out := make([][]uint8, len(b))
	for i, v := range b {
		if v == nil {
			out[i] = []uint8{}
		} else {
			out[i] = v
		}
	}
	return out
}
