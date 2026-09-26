package main

import (
	"errors"
	"fmt"
	"math/bits"
	"os"
	"sort"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/hashk"
	"ontology/ring"
)

var failed bool

func check(name string, ok bool) {
	s := "OK   "
	if !ok {
		s, failed = "FAIL ", true
	}
	fmt.Println(s + name)
}

// naive 朴素参照：有序虚节点里线性找首个 Pos >= keyPos，全无则回绕取 [0]。
func naive(snap []ring.VNode, keyPos uint32) uint32 {
	for _, vn := range snap {
		if vn.Pos >= keyPos {
			return vn.Node
		}
	}
	return snap[0].Node
}

func mustAPI(vnodes int) *api.API {
	a, err := api.New(vnodes)
	if err != nil {
		panic(err)
	}
	return a
}

func main() {
	// hashk：H 与虚节点位置，对照 NOTES.md 推导值
	check("hashk H/vnode positions", hashk.H(50) == 0xe6d5c492 &&
		hashk.VNodePos(1, 0) == 0x3779b100 && hashk.VNodePos(1, 1) == 0xd5b12ab1 &&
		hashk.VNodePos(2, 0) == 0x6ef36200 && hashk.VNodePos(2, 1) == 0x0d2adbb1 &&
		hashk.VNodePos(3, 0) == 0xa66d1300 && hashk.VNodePos(3, 1) == 0x44a48cb1)
	// ring：键 50 回绕到位置最小的虚节点（节点 2）
	r := ring.New(2)
	for _, id := range []uint32{1, 2, 3} {
		r.AddNode(id)
	}
	n50, ok50 := r.Successor(hashk.KeyPos(50))
	check("ring key50 wraps to node 2", ok50 && n50 == 2)
	// ring：RemoveNode(3) 后键 30/70/80 重映射为 1/2/1
	r.RemoveNode(3)
	n30, _ := r.Successor(hashk.KeyPos(30))
	n70, _ := r.Successor(hashk.KeyPos(70))
	n80, _ := r.Successor(hashk.KeyPos(80))
	check("ring RemoveNode(3) remap 30/70/80 -> 1/2/1", n30 == 1 && n70 == 2 && n80 == 1)
	// api：第三节八个键的归属节点（含键 50 回绕）
	a := mustAPI(2)
	for _, id := range []uint32{1, 2, 3} {
		a.AddNode(id)
	}
	okEight := true
	for k, w := range map[uint32]uint32{10: 1, 20: 2, 30: 3, 40: 1, 50: 2, 60: 1, 70: 3, 80: 3} {
		if got, err := a.Get(k); err != nil || got != w {
			okEight = false
		}
	}
	check("api eight keys ownership (key50 wraps)", okEight)
	// api：多档规模下 Get 与朴素参照逐键一致
	okNaive := true
	for _, nodes := range []int{3, 17, 100} {
		ra, aa := ring.New(7), mustAPI(7)
		for id := uint32(0); id < uint32(nodes); id++ {
			ra.AddNode(id*2654435 + 1)
			aa.AddNode(id*2654435 + 1)
		}
		snap := ra.Snapshot()
		for k := uint32(0); k < 500; k++ {
			key := k*2654435761 + 7
			if got, err := aa.Get(key); err != nil || got != naive(snap, hashk.KeyPos(key)) {
				okNaive = false
			}
		}
	}
	check("api Get == naive reference (multi-scale)", okNaive)
	// api：四类可判定错误互不相同，且被拒后状态不变
	empty := mustAPI(1)
	_, e0 := empty.Get(0)
	e1, e2 := a.AddNode(1), a.RemoveNode(99)
	_, e4 := api.New(0)
	distinct := errors.Is(e0, api.ErrEmptyRing) && errors.Is(e1, api.ErrNodeExists) &&
		errors.Is(e2, api.ErrNodeMissing) && errors.Is(e4, api.ErrInvalidVnodes) &&
		!errors.Is(e0, api.ErrNodeExists) && !errors.Is(e1, api.ErrNodeMissing) &&
		!errors.Is(e2, api.ErrNodeExists) && !errors.Is(e4, api.ErrEmptyRing)
	got50, err50 := a.Get(50)
	check("api 4 distinct sentinel errors + state unchanged", distinct &&
		a.NodeCount() == 3 && err50 == nil && got50 == 2)
	// api：SelfCheck 通过
	check("api SelfCheck", a.SelfCheck() == nil)
	// 复杂度：二分定位的比较次数 ≤ ceil(log2 m)+1，不随 m 线性增长
	okCmp := true
	for _, m := range []int{100, 1000, 10000} {
		rc := ring.New(1)
		for id := uint32(0); id < uint32(m); id++ {
			rc.AddNode(id*2654435 + 1)
		}
		snap, cmp := rc.Snapshot(), 0
		sort.Search(len(snap), func(i int) bool { cmp++; return snap[i].Pos >= hashk.KeyPos(42) })
		if cmp > bits.Len(uint(m))+1 {
			okCmp = false
		}
	}
	check("comparison count <= ceil(log2 m)+1 (m=100..10000)", okCmp)
	// 并发：16 个 goroutine 只读 Get 同一组键，结果一致且等于朴素参照
	ra, aa := ring.New(5), mustAPI(5)
	for id := uint32(0); id < 50; id++ {
		ra.AddNode(id*7919 + 3)
		aa.AddNode(id*7919 + 3)
	}
	snap := ra.Snapshot()
	okConc := atomic.Bool{}
	okConc.Store(true)
	var wg sync.WaitGroup
	for g := uint32(0); g < 16; g++ {
		wg.Add(1)
		go func(g uint32) {
			defer wg.Done()
			for k := uint32(0); k < 200; k++ {
				key := k*2246822519 + g
				if got, err := aa.Get(key); err != nil || got != naive(snap, hashk.KeyPos(key)) {
					okConc.Store(false)
				}
			}
		}(g)
	}
	wg.Wait()
	check("concurrent read-only Get consistent", okConc.Load())
	if failed {
		os.Exit(1)
	}
}
