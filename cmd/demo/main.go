package main

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/snapchain"
)

var nfail int

func fail(format string, a ...any) {
	nfail++
	fmt.Printf("FAIL "+format+"\n", a...)
}

func main() {
	// 第三节八步：逐步核验现存快照、删除文件与文件存储（对照 NOTES.md 推导表）
	g := api.New()
	ck := func(i int, del, wantDel, snaps, files string) bool {
		s, f := fmt.Sprint(g.Snapshots()), fmt.Sprint(g.Files())
		if s != snaps || f != files || del != wantDel {
			fail("step%d snaps=%s del=%s files=%s", i, s, del, f)
			return false
		}
		fmt.Printf("OK step%d snaps=%s del=%s files=%s\n", i, s, del, f)
		return true
	}
	g.Commit(10, []string{"f1", "f2"}, nil)
	ck(1, "[]", "[]", "[{1 10 [f1 f2]}]", "[f1 f2]")
	g.Commit(20, []string{"f3"}, []string{"f1"})
	ck(2, "[]", "[]", "[{1 10 [f1 f2]} {2 20 [f2 f3]}]", "[f1 f2 f3]")
	g.Commit(30, []string{"f4"}, []string{"f2"})
	ck(3, "[]", "[]", "[{1 10 [f1 f2]} {2 20 [f2 f3]} {3 30 [f3 f4]}]", "[f1 f2 f3 f4]")
	g.Commit(40, []string{"f5"}, []string{"f3"})
	ck(4, "[]", "[]", "[{1 10 [f1 f2]} {2 20 [f2 f3]} {3 30 [f3 f4]} {4 40 [f4 f5]}]", "[f1 f2 f3 f4 f5]")
	d, _ := g.Expire(1, 15)
	ok5 := ck(5, fmt.Sprint(d), "[f1]", "[{2 20 [f2 f3]} {3 30 [f3 f4]} {4 40 [f4 f5]}]", "[f2 f3 f4 f5]")
	g.Commit(50, []string{"f6"}, []string{"f4"})
	ck(6, "[]", "[]", "[{2 20 [f2 f3]} {3 30 [f3 f4]} {4 40 [f4 f5]} {5 50 [f5 f6]}]", "[f2 f3 f4 f5 f6]")
	d, _ = g.Expire(2, 35)
	ck(7, fmt.Sprint(d), "[f2 f3]", "[{4 40 [f4 f5]} {5 50 [f5 f6]}]", "[f4 f5 f6]")
	d, _ = g.Expire(0, 50)
	ok8 := ck(8, fmt.Sprint(d), "[f4]", "[{5 50 [f5 f6]}]", "[f5 f6]")

	// 随机序列对照朴素参照；每步校验保留快照可读且无泄漏
	g2, rng := api.New(), rand.New(rand.NewSource(9))
	var cur []string
	ts, randOK, leakOK := int64(0), true, true
	for i := 0; i < 200; i++ {
		if i == 0 || rng.Intn(3) > 0 {
			ts += 1 + rng.Int63n(9)
			add := fmt.Sprintf("g%d", i)
			var rmv []string
			if len(cur) > 0 && rng.Intn(2) == 0 {
				j := rng.Intn(len(cur))
				rmv = []string{cur[j]}
				cur = append(cur[:j], cur[j+1:]...)
			}
			g2.Commit(ts, []string{add}, rmv)
			cur = append(cur, add)
		} else {
			num, tt := rng.Intn(4), ts-rng.Int63n(30)
			wantKept, wantDel := snapchain.NaiveExpire(g2.Snapshots(), num, tt)
			del, _ := g2.Expire(num, tt)
			if fmt.Sprint(del) != fmt.Sprint(wantDel) || fmt.Sprint(g2.Snapshots()) != fmt.Sprint(wantKept) {
				randOK = false
			}
		}
		union := map[string]bool{}
		for _, sn := range g2.Snapshots() {
			for _, f := range sn.Files {
				union[f] = true
			}
		}
		if len(union) != len(g2.Files()) {
			leakOK = false
		}
	}
	if ok5 && ok8 && randOK && leakOK {
		fmt.Println("OK union-not-intersect current-never-expire random-vs-naive readable+noleak")
	} else {
		fail("union/current/random/leak: %v %v %v %v", ok5, ok8, randOK, leakOK)
	}

	selfOK := api.New().SelfCheck() == nil // 四类可判定错误 + 被拒后状态不变

	// 大 m：S1{f0}、S2{m 个新文件}，Expire 只删 f0（访问个数证明见 fileref 测试）
	g4 := api.New()
	g4.Commit(1, []string{"f0"}, nil)
	add := make([]string, 10000)
	for i := range add {
		add[i] = fmt.Sprintf("m%d", i)
	}
	g4.Commit(2, add, []string{"f0"})
	del4, _ := g4.Expire(1, math.MaxInt64)
	bigOK := fmt.Sprint(del4) == "[f0]" && len(g4.Files()) == 10000

	// 并发读一致：双读之间无写时，快照引用的文件都在同一次读到的文件集合里
	g5 := api.New()
	var wg sync.WaitGroup
	var bad, stop atomic.Bool
	for r := 0; r < 2; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				s1 := g5.Snapshots()
				have := map[string]bool{}
				for _, f := range g5.Files() {
					have[f] = true
				}
				if fmt.Sprint(s1) != fmt.Sprint(g5.Snapshots()) {
					continue
				}
				for _, sn := range s1 {
					for _, f := range sn.Files {
						if !have[f] {
							bad.Store(true)
							return
						}
					}
				}
			}
		}()
	}
	for i := 0; i < 200; i++ {
		g5.Commit(int64(i+1), []string{fmt.Sprintf("c%d", i)}, nil)
		if i%7 == 6 {
			g5.Expire(2, int64(i))
		}
	}
	stop.Store(true)
	wg.Wait()
	if selfOK && bigOK && !bad.Load() {
		fmt.Println("OK errors-4-kind reject-no-mutate big-m concurrent-consistent")
	} else {
		fail("selfcheck/big-m/concurrent: %v %v %v", selfOK, bigOK, bad.Load())
	}
	if nfail > 0 {
		os.Exit(1)
	}
}
