package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/vv"
)

func v(xs ...int) vv.Vector { return vv.Vector(xs) }

func report(name string, ok bool) bool {
	if ok {
		fmt.Println("OK   " + name)
	} else {
		fmt.Println("FAIL " + name)
	}
	return ok
}

func main() {
	allOK := true

	// 第三节八步：逐步 Stable/store[k]，第5步不回收、第6步 stale 写忽略、第8步回收 k。
	allOK = eightSteps() && allOK

	// 字典序覆盖：(1,0) 字典序大于 (0,9) 应覆盖；更旧向量再写被忽略。
	e, _ := api.New(2)
	_ = e.Write(0, "x", "lo", v(0, 9))
	_ = e.Write(0, "x", "hi", v(1, 0))
	_ = e.Write(1, "x", "old", v(0, 9))
	allOK = report("lexicographic override (0,9)<(1,0), stale ignored", e.View()["x"].Val == "hi") && allOK

	// 稳定向量单调 + GC 只回收 vec<=Stable：(2,0) 墓碑在 Stable 未达时保留，达后回收。
	g, _ := api.New(2)
	_ = g.Delete(0, "d", v(2, 0))
	before, mono := g.Stable(), true
	allOK = report("tomb kept while Stable=(0,0)", len(g.GC()) == 0) && allOK
	_ = g.Sync(1, v(2, 0))
	for i := range g.Stable() {
		mono = mono && g.Stable()[i] >= before[i]
	}
	allOK = report("Stable monotonic & GC only vec<=Stable -> [d]",
		mono && reflect.DeepEqual(g.GC(), []string{"d"})) && allOK

	// 四类可判定、互不相同的哨兵错误。
	_, errR := api.New(0)
	e2, _ := api.New(2)
	errRep := e2.Write(5, "q", "z", v(0, 0))
	errVec := e2.Write(0, "q", "z", v(-1, 0))
	errKey := e2.Write(0, "", "z", v(1, 0))
	distinct := map[error]bool{}
	for _, x := range []error{api.ErrInvalidR, api.ErrReplica, api.ErrVector, api.ErrKey} {
		distinct[x] = true
	}
	allOK = report("four distinct sentinel errors",
		errors.Is(errR, api.ErrInvalidR) && errors.Is(errRep, api.ErrReplica) &&
			errors.Is(errVec, api.ErrVector) && errors.Is(errKey, api.ErrKey) && len(distinct) == 4) && allOK

	// 被拒操作不留痕：错误前后 View 与 Stable 完全一致，之后仍可正常使用。
	snap := fmt.Sprint(e2.View(), e2.Stable())
	allOK = report("rejected op leaves no trace, instance still usable",
		fmt.Sprint(e2.View(), e2.Stable()) == snap && e2.Write(0, "ok", "1", v(1, 0)) == nil) && allOK

	// 大 m：m 个墓碑均严格大于当时 Stable，GC 返回 0；Stable 推过堆顶 1 条后恰好回收 1 条。
	// 只观察外部行为，不读取非导出的检查计数（计数器经 gc 包内测试断言有界）。
	big, _ := api.New(2)
	const m = 10000
	for i := 1; i <= m; i++ {
		_ = big.Delete(0, fmt.Sprintf("k%d", i), v(1, i))
	}
	first := len(big.GC()) == 0
	_ = big.Sync(1, v(1, 1))
	r := big.GC()
	allOK = report("heap GC bounded at m=10000: remove 0 then exactly 1",
		first && len(r) == 1 && r[0] == "k1") && allOK

	// 并发只读：N 个 goroutine 的 View 必须逐字段相同。
	allOK = concurrentViews() && allOK

	// SelfCheck 内置序列核验四条不变量。
	sc, _ := api.New(1)
	allOK = report("SelfCheck passes", sc.SelfCheck() == nil) && allOK

	if !allOK {
		os.Exit(1)
	}
}

func eightSteps() bool {
	e, _ := api.New(3)
	wantStable := []vv.Vector{v(0, 0, 0), v(0, 0, 0), v(0, 0, 0), v(0, 0, 0),
		v(0, 0, 0), v(1, 0, 0), v(1, 1, 0), v(1, 1, 0)}
	ok := true
	step := func(n int) { ok = ok && reflect.DeepEqual(e.Stable(), wantStable[n]) }
	ok = e.Write(0, "k", "a", v(1, 0, 0)) == nil && ok
	step(0)
	ok = e.Sync(1, v(1, 0, 0)) == nil && ok
	step(1)
	ok = e.Delete(1, "k", v(1, 1, 0)) == nil && ok
	step(2)
	ok = e.Sync(0, v(1, 1, 0)) == nil && ok
	step(3)
	gc5 := e.GC()
	ok = ok && len(gc5) == 0
	step(4) // 第5步：Stable=(0,0,0)，墓碑 (1,1,0) 不可回收
	_ = e.Write(2, "k", "stale", v(1, 0, 0))
	ent, present := e.View()["k"]
	ok = ok && present && ent.Tomb && reflect.DeepEqual(ent.Vec, v(1, 1, 0))
	step(5) // 第6步 stale 写忽略，仍是 (1,1,0) 墓碑
	ok = e.Sync(2, v(1, 1, 0)) == nil && ok
	step(6)
	gc8 := e.GC()
	ok = ok && reflect.DeepEqual(gc8, []string{"k"})
	step(7)
	ok = ok && len(e.View()) == 0 // 第8步回收 k
	return report("eight-step scenario (Stable/store[k]/GC5/stale-write/GC8)", ok)
}

func concurrentViews() bool {
	e, _ := api.New(3)
	for i := 0; i < 60; i++ {
		_ = e.Write(i%3, fmt.Sprintf("k%d", i), "v", v(i%4, (i*2)%4, (i*3)%4))
	}
	want := e.View()
	var wg sync.WaitGroup
	same := true
	var mu sync.Mutex
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if !reflect.DeepEqual(e.View(), want) {
					mu.Lock()
					same = false
					mu.Unlock()
					return
				}
				_ = e.Stable()
			}
		}()
	}
	wg.Wait()
	return report("concurrent readers see identical View", same)
}
