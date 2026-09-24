package main

import (
	"fmt"
	"math/rand"
	"sync"

	"ontology/snapshot"
	"ontology/txn"
	"ontology/visible"
)

var fails int

// naive 是朴素参考：只比提交号 < 水位，不看活跃集，且遍历事务表。
func naive(r *txn.Registry, s *snapshot.Snapshot, t txn.ID) bool {
	for _, id := range r.All() {
		if st, c, _ := r.Lookup(id); id == t && st == txn.Committed && c < s.Watermark() {
			return true
		}
	}
	return false
}

func line(name string, ok bool) {
	tag := "OK"
	if !ok {
		tag, fails = "FAIL", fails+1
	}
	fmt.Printf("%s %s\n", tag, name)
}

func main() {
	// 反例：T1 提交号 5、水位 10、活跃集含 T1。
	r := txn.NewRegistry()
	t1 := r.Begin()
	r.Commit(t1, 5)
	s, _ := snapshot.New(10, []txn.ID{t1}, 999, 0)
	d, _ := visible.Decide(r, s, visible.Version{Txn: t1})
	line("naive says visible, real impl says invisible", naive(r, s, t1) && !d.Visible)

	eq, _ := snapshot.New(5, nil, 999, 0)
	de, _ := visible.Decide(r, eq, visible.Version{Txn: t1})
	line("commit number == watermark invisible", !de.Visible)

	own := txn.NewRegistry()
	me := own.Begin()
	own.Commit(me, 1)
	os, _ := snapshot.New(0, nil, me, 0)
	do, _ := visible.Decide(own, os, visible.Version{Txn: me})
	line("own write visible to self", do.Visible)

	ab := txn.NewRegistry()
	x := ab.Begin()
	ab.Abort(x)
	as, _ := snapshot.New(100, nil, 999, 0)
	da, _ := visible.Decide(ab, as, visible.Version{Txn: x})
	line("aborted never visible", !da.Visible)

	// 1 万事务、活跃集 1 千；单次判定查找次数 <= 2。
	big := txn.NewRegistry()
	for i := 0; i < 10_000; i++ {
		id := big.Begin()
		if i >= 1000 {
			big.Commit(id, uint64(i))
		}
	}
	act := make([]txn.ID, 1000)
	for i := range act {
		act[i] = txn.ID(i)
	}
	bs, _ := snapshot.New(5000, act, 999, 0)
	db, _ := visible.Decide(big, bs, visible.Version{Txn: 5000})
	line("single decision uses <= 2 lookups", db.Lookups() <= 2)

	_, err := snapshot.New(10, []txn.ID{1, 2, 3}, 999, 2)
	line("oversized active set rejected", err == snapshot.ErrActiveSetLimit)
	line("snapshot memory items == active set size", bs.Len() == 1000)

	// 固定种子 1 万组随机对拍：正式实现 vs 朴素参考。
	match := true
	rnd := rand.New(rand.NewSource(42))
	for i := 0; i < 10_000; i++ {
		id := txn.ID(rnd.Intn(10_000))
		if st, _, _ := big.Lookup(id); st == txn.Active {
			continue
		}
		dd, derr := visible.Decide(big, bs, visible.Version{Txn: id})
		if derr == nil && dd.Visible != naive(big, bs, id) {
			match = false
		}
	}
	line("10k randomized cross-check matches naive", match)

	// 并发只读：结果逐个相同，计数器互不串台。
	const g = 32
	var wg sync.WaitGroup
	res, lks := make([]bool, g), make([]int, g)
	one := func(i int) {
		defer wg.Done()
		dd, _ := visible.Decide(big, bs, visible.Version{Txn: 5000})
		res[i], lks[i] = dd.Visible, dd.Lookups()
	}
	for i := 0; i < g; i++ {
		wg.Add(1)
		go one(i)
	}
	wg.Wait()
	okc := true
	for i := 1; i < g; i++ {
		if res[i] != res[0] || lks[i] != lks[0] {
			okc = false
		}
	}
	line("concurrent decisions consistent", okc)

	if fails == 0 {
		fmt.Println("TOTAL: all 9 checks OK")
	} else {
		fmt.Printf("TOTAL: %d FAIL\n", fails)
	}
}
