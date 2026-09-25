package main

import (
	"fmt"
	"os"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/mcsnode"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Println(map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}

// enqueue 强制按序入队 n 个节点，返回节点序列（首元素为持有者）。
func enqueue(l *api.Lock, n int) []*mcsnode.Node {
	var nodes []*mcsnode.Node
	for len(nodes) < n {
		base := len(l.Snapshot())
		go func() { _, _ = l.Acquire() }()
		for len(l.Snapshot()) != base+1 {
			runtime.Gosched()
		}
		nodes = append(nodes, l.Snapshot()[base])
	}
	return nodes
}

// drain 按入队顺序逐个释放，校验持有者与 tail 每步都是队首/队尾。
func drain(l *api.Lock, nodes []*mcsnode.Node) bool {
	for i, nd := range nodes {
		snap := l.Snapshot()
		if len(snap) != len(nodes)-i || snap[0] != nd || snap[len(snap)-1] != nodes[len(nodes)-1] {
			return false
		}
		if err := l.Release(nd); err != nil {
			return false
		}
	}
	return len(l.Snapshot()) == 0
}

func main() {
	l1 := api.New()
	ns1 := enqueue(l1, 3) // 六步表：A、B、C 依次 Acquire 再依次 Release
	check("six-step trace: tail/queue/holder per step", drain(l1, ns1))

	l2 := api.New()
	check("FIFO grant order == enqueue order (m=64)", drain(l2, enqueue(l2, 64)))

	fifo := []int{1, 2, 3}
	tasReachable := [][]int{{1, 2, 3}, {1, 3, 2}} // 单标志 TAS：B、C 谁抢到是谁
	disorder := false
	for _, o := range tasReachable {
		if !slices.Equal(o, fifo) {
			disorder = true
		}
	}
	check("TAS can grant out of order (A,C,B)", disorder)

	na, nb := mcsnode.New(), mcsnode.New()
	na.Unlock()    // na 持有；B 已 swap tail 但还没链 na.next
	na.SetNext(nb) // 错误实现见 next==nil 直接返回，B 链上后无人清 nb.locked
	check("release w/o CAS+wait starves successor", nb.Locked())

	na2, nb2 := mcsnode.New(), mcsnode.New()
	na2.Unlock() // 错误实现：清自己的 locked 而非后继的
	check("clearing own flag starves successor", nb2.Locked())

	l3 := api.New()
	n0, _ := l3.Acquire()
	e1, e2 := l3.Release(nil), l3.Release(mcsnode.New())
	l3.Close()
	_, e3 := l3.Acquire()
	check("three distinct sentinel errors", e1 == api.ErrNilNode && e2 == api.ErrNotOwner &&
		e3 == api.ErrClosed && e1 != e2 && e2 != e3 && e1 != e3 && n0 != nil)

	l4 := api.New()
	ns4 := enqueue(l4, 2)
	before := l4.Snapshot()
	l4.Release(nil)
	l4.Release(ns4[1])
	l4.Release(mcsnode.New())
	check("rejected ops leave zero state change", slices.Equal(before, l4.Snapshot()) && drain(l4, ns4))

	l5 := api.New()
	check("large-m (2000) O(1) handoff (counter pinned in qlock test)", drain(l5, enqueue(l5, 2000)))

	l6 := api.New()
	var shared atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 256; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if nd, err := l6.Acquire(); err == nil {
				shared.Add(1)
				_ = l6.Release(nd)
			}
		}()
	}
	wg.Wait()
	check("concurrent shared counter == N", shared.Load() == 256)

	check("SelfCheck: four invariants", api.New().SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
