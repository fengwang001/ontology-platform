// demo 演示时间旅行读：八步脚本、错误语义、不变量、并发。退出码 0 表示全部通过。
package main

import (
	"fmt"
	"maps"
	"math/rand/v2"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
)

var failed bool

type wr struct {
	seq int64
	key string
	val int
}

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Println(name, map[bool]string{true: "OK", false: "FAIL"}[ok])
}

func writes(e *api.Engine, kvs ...any) {
	for i := 0; i < len(kvs); i += 2 {
		e.Write(kvs[i].(string), kvs[i+1])
	}
}

func main() {
	// 1. 第三节八步脚本：写 a=1@1 b=10@2 a=2@3，读 0..4，Compact(2)，再读 2,3,4。
	e := api.New()
	writes(e, "a", 1, "b", 10, "a", 2)
	want1 := []map[string]api.Val{{}, {"a": 1}, {"a": 1, "b": 10}, {"a": 2, "b": 10}, {"a": 2, "b": 10}}
	ok := true
	for s, w := range want1 {
		got, err := e.AsOf(int64(s))
		ok = ok && err == nil && maps.Equal(got, w)
	}
	e.Compact(2)
	_, err2 := e.AsOf(2)
	got3, _ := e.AsOf(3)
	got4, _ := e.AsOf(4)
	ok = ok && err2 == api.ErrCompacted && maps.Equal(got3, map[string]api.Val{"a": 2, "b": 10}) && maps.Equal(got4, map[string]api.Val{"a": 2, "b": 10})
	check("eight-step reads (5 pre + 3 post compact)", ok)
	check("post-Compact(2) AsOf(3) keeps b=10", got3["b"] == 10)
	check("post-Compact(2) AsOf(2) unreachable", err2 == api.ErrCompacted)

	// 2. Compact(3)：a 的基线版本取值应为 2（Seq=3 整体降为基线），经 AsOf(4) 观察。
	e2 := api.New()
	writes(e2, "a", 1, "b", 10, "a", 2)
	e2.Compact(3)
	v4, err4 := e2.AsOf(4)
	_, err3 := e2.AsOf(3)
	check("Compact(3): baseline a=2, AsOf(3) compacted",
		err3 == api.ErrCompacted && err4 == nil && v4["a"] == 2 && v4["b"] == 10)

	// 3. 随机操作序列与朴素重放逐 key 一致。
	rng := rand.New(rand.NewPCG(1, 2))
	e3 := api.New()
	var ws []wr
	upto := int64(-1)
	ok = true
	for i := 0; i < 200; i++ {
		if rng.IntN(4) == 0 {
			if u := upto + 1 + int64(rng.IntN(3)); e3.Compact(u) == nil && u > upto {
				upto = u
			}
			continue
		}
		k, v := string(rune('a'+rng.IntN(5))), rng.IntN(100)
		s, _ := e3.Write(k, v)
		ws = append(ws, wr{s, k, v})
	}
	for s := upto + 1; s <= e3.MaxSeq()+2; s++ {
		got, err := e3.AsOf(s)
		want := map[string]api.Val{}
		for _, w := range ws {
			if w.seq <= s {
				want[w.key] = w.val
			}
		}
		ok = ok && err == nil && maps.Equal(got, want)
	}
	check("random ops match naive replay", ok)

	// 4. 四类可判定错误互不相同；被拒后状态不变（含被拒写入不分配 Seq）。
	e4 := api.New()
	writes(e4, "x", 1)
	e4.Compact(1)
	m0 := e4.MaxSeq()
	snap, _ := e4.AsOf(2)
	_, eA := e4.Write("", 9)
	_, eB := e4.AsOf(-1)
	_, eC := e4.AsOf(1)
	eD := e4.Compact(-1)
	distinct := eA != eB && eA != eC && eA != eD && eB != eC && eB != eD && eC != eD
	check("four distinct sentinel errors", eA == api.ErrEmptyKey && eB == api.ErrNegativeRead && eC == api.ErrCompacted && eD == api.ErrNegativeCompact && distinct)
	after, _ := e4.AsOf(2)
	s5, _ := e4.Write("y", 2)
	check("rejected ops leave no trace", e4.MaxSeq() == m0+1 && s5 == m0+1 && maps.Equal(snap, after))

	// 5. 大 m 下单 key 读为常数复杂度（计数器为非导出字段，由 hist 包内测试钉住）。
	e5 := api.New()
	for i := 0; i < 10000; i++ {
		e5.Write("hot", i)
		if i%7 == 0 {
			e5.Write("cold", i)
		}
	}
	vHot, errH := e5.AsOf(e5.MaxSeq())
	check("large-m latest read correct (O(1) pinned by hist test)", errH == nil && vHot["hot"] == 9999)

	// 6. 并发只读：compact 掉前一半位点后，N 个 goroutine 读同一位点结果一致。
	e6 := api.New()
	for i := 0; i < 50; i++ {
		e6.Write(string(rune('a'+i%5)), i)
	}
	e6.Compact(25)
	want, _ := e6.AsOf(40)
	var wg sync.WaitGroup
	start := make(chan struct{})
	var okc atomic.Bool
	okc.Store(true)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if got, err := e6.AsOf(40); err != nil || !maps.Equal(got, want) {
				okc.Store(false)
			}
		}()
	}
	close(start)
	wg.Wait()
	check("concurrent reads identical", okc.Load())
	check("SelfCheck", api.New().SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
