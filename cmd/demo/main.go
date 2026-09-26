package main

import (
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"os"
	"reflect"
	"slices"
	"sync"
	"unsafe"

	"ontology/api"
	"ontology/rr"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s: %s\n", name, map[bool]string{true: "OK", false: "FAIL"}[ok])
}

// naiveSim 逐 tick 朴素参照：每 tick 队头减 1，减到 0 完成，计满 quantum 移到队尾；ps 须已按 (arrival, pid) 升序。
func naiveSim(quantum int64, ps [][3]int64) map[int64]int64 {
	type pr struct{ pid, arrival, rem int64 }
	all := make([]pr, 0, len(ps))
	for _, p := range ps {
		all = append(all, pr{p[0], p[1], p[2]})
	}
	done := make(map[int64]int64, len(all))
	var q []int
	next, t, used := 0, int64(0), int64(0)
	enq := func() { // arrival <= t 的进程入队尾
		for next < len(all) && all[next].arrival <= t {
			q = append(q, next)
			next++
		}
	}
	for len(done) < len(all) {
		if len(q) == 0 {
			t = all[next].arrival
		}
		enq()
		h := q[0]
		all[h].rem--
		t++
		used++
		if all[h].rem == 0 {
			done[all[h].pid] = t
			q, used = q[1:], 0
		} else if used == quantum {
			q = q[1:]
			enq() // 同时刻新到进程排在被抢占者之前
			q, used = append(q, h), 0
		}
	}
	return done
}

func main() {
	// 第三节：quantum=4 四进程时间片表，完成时刻 P1=19 P2=8 P3=11 P4=13。
	s, _ := api.New(4)
	for _, p := range [][3]int64{{1, 0, 10}, {2, 1, 4}, {3, 3, 3}, {4, 3, 2}} {
		_ = s.Add(p[0], p[1], p[2])
	}
	got, _ := s.Run()
	check("slice table P1=19 P2=8 P3=11 P4=13",
		reflect.DeepEqual(got, map[int64]int64{1: 19, 2: 8, 3: 11, 4: 13}))

	// 多组随机进程（arrival 单调生成即已排序）：与朴素参照一致、完成时刻合法。
	rng, refOK, validOK := rand.New(rand.NewSource(1)), true, true
	for i := 0; i < 20; i++ {
		q, n, arr := 1+rng.Int63n(8), 1+rng.Intn(8), int64(0)
		ps := make([][3]int64, n)
		sc, _ := api.New(q)
		for j := range ps {
			arr += rng.Int63n(4)
			ps[j] = [3]int64{int64(j + 1), arr, 1 + rng.Int63n(20)}
			_ = sc.Add(ps[j][0], ps[j][1], ps[j][2])
		}
		g, _ := sc.Run()
		refOK = refOK && reflect.DeepEqual(g, naiveSim(q, ps))
		seen := map[int64]bool{}
		for _, p := range ps {
			validOK = validOK && g[p[0]] >= p[1]+p[2] && !seen[g[p[0]]]
			seen[g[p[0]]] = true
		}
	}
	check("matches naive reference", refOK)
	check("completion >= arrival+burst, distinct", validOK)

	// 守恒：全部同时到达时 CPU 不空转，makespan == sum(burst) = 10。
	z, _ := api.New(3)
	for _, p := range [][3]int64{{1, 0, 3}, {2, 0, 5}, {3, 0, 2}} {
		_ = z.Add(p[0], p[1], p[2])
	}
	zg, _ := z.Run()
	check("conservation makespan==sum(burst)", slices.Max(slices.Collect(maps.Values(zg))) == 10)

	// 三类可判定错误，互不相同。
	a, _ := api.New(3)
	ok := a.Add(1, 0, 2) == nil
	eProc, eDup := a.Add(2, -1, 1), a.Add(1, 5, 5)
	_, eQ := api.New(0)
	ok = ok && errors.Is(eProc, api.ErrInvalidProcess) && !errors.Is(eProc, api.ErrDuplicatePID) &&
		errors.Is(eDup, api.ErrDuplicatePID) && !errors.Is(eDup, api.ErrInvalidProcess) &&
		errors.Is(eQ, api.ErrInvalidQuantum) && !errors.Is(eQ, api.ErrInvalidProcess)
	check("3 distinguishable sentinel errors", ok)

	// 被拒后状态不变，仍可正常使用。
	g1, _ := a.Run()
	g2, _ := a.Run()
	ok = len(g1) == 1 && g1[1] == 2 && reflect.DeepEqual(g1, g2) && a.Add(2, 0, 1) == nil
	g3, _ := a.Run()
	check("rejected ops leave no trace", ok && len(g3) == 2)

	// 大 m 下逐单位推进计数器不随 m 增长（恒为 0，经反射读非导出字段）。
	ticksOK := true
	for _, m := range []int64{100, 1000, 10000} {
		rs, _ := rr.New(m)
		_ = rs.Add(1, 0, m)
		rs.Run()
		f := reflect.ValueOf(rs).Elem().FieldByName("ticks")
		ticksOK = ticksOK && reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Int() == 0
	}
	check("block-advance counter flat in m", ticksOK)

	// 并发 Add：N 个 goroutine 各登记一个进程，完成时刻数量恰为 N 且值正确。
	const n = 64
	cs, _ := api.New(3)
	cps := make([][3]int64, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		cps[i] = [3]int64{int64(i + 1), 0, int64(1 + i%5)}
		wg.Add(1)
		go func(p [3]int64) { defer wg.Done(); _ = cs.Add(p[0], p[1], p[2]) }(cps[i])
	}
	wg.Wait()
	cg, _ := cs.Run()
	check("concurrent Add N completions", len(cg) == n && reflect.DeepEqual(cg, naiveSim(3, cps)))

	if failed {
		os.Exit(1)
	}
}
