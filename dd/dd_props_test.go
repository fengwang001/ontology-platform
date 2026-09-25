package dd_test

import (
	"maps"
	"sync"
	"testing"

	"ontology/dd"
)

type tuple = [2]any

// 第三节八步操作。
var eDel = [...]bool{false, false, false, false, false, true, false, true}
var eID = [...]int{1, 2, 3, 4, 1, 4, 2, 3}
var eC1 = [...]string{"a", "a", "b", "a", "a", "", "b", ""}
var eC2 = [...]int{10, 20, 10, 10, 30, 0, 10, 0}

// replay 以朴素引用计数重放八步；noRetract（1 基步号）指定某步 Delete 不撤回。
func replay(noRetract int) [8]map[tuple]int {
	var out [8]map[tuple]int
	refs, cur := map[tuple]int{}, map[int]tuple{}
	for i := range eID {
		tp := tuple{eC1[i], eC2[i]}
		if eDel[i] {
			if noRetract != i+1 {
				refs[cur[eID[i]]]--
			}
			delete(cur, eID[i])
		} else {
			if o, ok := cur[eID[i]]; ok {
				refs[o]--
			}
			cur[eID[i]], refs[tp] = tp, refs[tp]+1
		}
		out[i] = maps.Clone(refs)
	}
	return out
}
func stats(m map[tuple]int) (p, s int) {
	for _, c := range m {
		if c > 0 {
			p++
		}
		s += c
	}
	return
}
func TestEightStepDerivation(t *testing.T) {
	// 钉死 NOTES.md 八行表；甲/乙/丙 错值由模型快照真实算出。
	s, e := replay(0), dd.NewEngine()
	for i := range eID {
		var err error
		if eDel[i] {
			err = e.Delete(eID[i])
		} else {
			err = e.Upsert(eID[i], "k", eC1[i], eC2[i])
		}
		d, _ := stats(s[i])
		if err != nil || e.Distinct("k") != d {
			t.Fatalf("step %d: engine=%d model=%d", i+1, e.Distinct("k"), d)
		}
	}
	if e.Distinct("k") != 2 || e.Total() != 2 {
		t.Fatal("final must be 2/2")
	}
	if eC1[0] != "a" || eC1[1] != "a" { // (甲) 单列 Col1 去重：两行都是 a → 错 1（正确 2）
		t.Fatal("(甲) setup")
	}
	bad := replay(6) // (乙) Delete 不撤回 → (a,10) 保留 → 错 4（正确 3）
	if n, _ := stats(bad[5]); n != 4 || bad[5][tuple{"a", 10}] != 1 {
		t.Fatalf("(乙) %d stale(a,10)=%d", n, bad[5][tuple{"a", 10}])
	}
	if _, rows := stats(s[3]); rows != 4 { // (丙) 不去重：活跃 4 行 → 错 4（正确 3）
		t.Fatal("(丙) no-dedup=4")
	}
}
func TestRetractSymmetry(t *testing.T) {
	// 表驱动钉死撤回对称（不变量 3）：旧恰减 1、新恰加 1。
	type op struct {
		id, c2  int
		key, c1 string
		del     bool
	}
	U := func(id int, k, c1 string, c2 int) op { return op{id: id, key: k, c1: c1, c2: c2} }
	D := func(id int) op { return op{id: id, del: true} }
	do := func(e *dd.Engine, o op) error {
		if o.del {
			return e.Delete(o.id)
		}
		return e.Upsert(o.id, o.key, o.c1, o.c2)
	}
	cases := []struct {
		hist []op
		last op
		want int
	}{
		{[]op{U(1, "k", "a", 1), U(2, "k", "a", 1)}, U(2, "k", "a", 1), 1},
		{[]op{U(1, "k", "a", 1), U(2, "k", "a", 1)}, U(1, "k", "b", 2), 2},
		{[]op{U(1, "k", "a", 1)}, U(1, "j", "a", 1), 1},
		{[]op{U(1, "k", "a", 1)}, D(1), 0},
	}
	for i, c := range cases {
		e := dd.NewEngine()
		for _, o := range c.hist {
			if err := do(e, o); err != nil {
				t.Fatal(err)
			}
		}
		if err := do(e, c.last); err != nil || e.Total() != c.want {
			t.Fatalf("case %d total=%d want %d", i, e.Total(), c.want)
		}
	}
}
func TestConcurrentUpsert(t *testing.T) {
	// N goroutine 各写唯一 rowID、不同元组；结束 Distinct/Total==N，期间并发读
	// 单调不减、SelfCheck 恒通过；无 sleep。
	const N = 128
	e, stop, start := dd.NewEngine(), make(chan struct{}), make(chan struct{})
	var rg, wg sync.WaitGroup
	rg.Add(1)
	go func() {
		defer rg.Done()
		prev := 0
		for {
			select {
			case <-stop:
				return
			default:
				v := e.Distinct("k")
				if v < prev || e.Total() < v || e.SelfCheck() != nil {
					t.Errorf("bad read %d->%d", prev, v)
				}
				prev = v
			}
		}
	}()
	for i := 1; i <= N; i++ {
		wg.Add(1)
		go func(id int) { defer wg.Done(); <-start; _ = e.Upsert(id, "k", "c", id) }(i)
	}
	close(start)
	wg.Wait()
	close(stop)
	rg.Wait()
	if e.Distinct("k") != N || e.Total() != N {
		t.Fatalf("d=%d total=%d want %d", e.Distinct("k"), e.Total(), N)
	}
}
