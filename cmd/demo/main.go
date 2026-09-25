package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/bnode"
	"ontology/btree"
)

var failed bool

func report(ok bool, label string) {
	if ok {
		fmt.Println("OK " + label)
	} else {
		failed = true
		fmt.Println("FAIL " + label)
	}
}

func seq(n int) []int {
	s := make([]int, n)
	for i := range s {
		s[i] = i + 1
	}
	return s
}

func leaves(n *bnode.Node) (out [][]int) {
	if n.Leaf() {
		return append(out, n.Keys)
	}
	for _, c := range n.Children {
		out = append(out, leaves(c)...)
	}
	return out
}

func main() {
	root := &bnode.Node{Keys: []int{2}, Children: []*bnode.Node{
		{Keys: []int{1}}, {Keys: []int{3, 4}},
	}}
	report(reflect.DeepEqual(root.Inorder(nil), []int{1, 2, 3, 4}), "bnode 中序升序")

	// 第三节七步推导：空树升序插入 1..7
	tr := btree.New(100)
	wantRoots := [][]int{{1}, {1, 2}, {2}, {2}, {2, 4}, {2, 4}, {4}}
	total, c7, ok7 := 0, 0, true
	for k := 1; k <= 7; k++ {
		c, _ := tr.Insert(k)
		total, c7 = total+c, c
		ok7 = ok7 && reflect.DeepEqual(tr.Root.Keys, wantRoots[k-1]) &&
			reflect.DeepEqual(tr.Root.Inorder(nil), seq(k))
	}
	report(ok7, "七步: 根序列 [1][1 2][2][2][2 4][2 4][4], 中序升序")
	report(c7 == 2 && total == 4 && reflect.DeepEqual(tr.Root.Keys, []int{4}),
		"第7步根=[4] 代价=2 总分裂=4")
	cDel, _ := tr.Delete(3)
	report(cDel == 2 && reflect.DeepEqual(tr.Root.Keys, []int{4, 6}) &&
		reflect.DeepEqual(leaves(tr.Root), [][]int{{1, 2}, {5}, {7}}),
		"Delete(3): 合并=2 根=[4 6] 叶=[1 2][5][7]")

	// api：Search 与二分参照一致、OrderedKeys 升序无重
	a, _ := api.New(20000)
	ref := map[int]bool{}
	rng := rand.New(rand.NewSource(1))
	for len(ref) < 1000 {
		if k := rng.Intn(5000); !ref[k] {
			ref[k] = true
			a.Insert(k)
		}
	}
	for k := range ref {
		if k%7 == 0 {
			if _, err := a.Delete(k); err == nil {
				delete(ref, k)
			}
		}
	}
	keys := a.OrderedKeys()
	okRef := slices.IsSorted(keys) && len(keys) == len(ref)
	for probe := 0; probe < 5000; probe += 97 {
		_, want := slices.BinarySearch(keys, probe)
		okRef = okRef && a.Search(probe) == want
	}
	report(okRef, "Search==二分参照, OrderedKeys 升序无重")

	// 三类可判定错误互不相同，被拒后结构不变
	small, _ := api.New(3)
	for _, k := range []int{1, 2, 3} {
		small.Insert(k)
	}
	before := small.OrderedKeys()
	_, e1 := small.Insert(2)
	_, e2 := small.Delete(99)
	_, e3 := small.Insert(4)
	okErr := errors.Is(e1, api.ErrDuplicate) && errors.Is(e2, api.ErrNotFound) &&
		errors.Is(e3, api.ErrFull) && e1 != e2 && e2 != e3 && e1 != e3
	report(okErr && slices.Equal(small.OrderedKeys(), before),
		"三类哨兵错误可区分, 被拒后结构不变")
	report(a.SelfCheck() == nil && small.SelfCheck() == nil, "SelfCheck 四条不变量")

	// 大 m 下查找沿树高下降（visited 计数器由 btree 包内测试钉住）
	big, _ := api.New(20000)
	for i := 1; i <= 10000; i++ {
		big.Insert(i)
	}
	report(big.Height() <= 40 && big.Search(7777), "m=10000 树高<=40, 查找不随m线性增长")

	// 并发插入/查找一致
	c, _ := api.New(1 << 20)
	var wg sync.WaitGroup
	var okConc atomic.Bool
	okConc.Store(true)
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				k := g*50 + i
				if _, err := c.Insert(k); err != nil || !c.Search(k) {
					okConc.Store(false)
				}
			}
		}(g)
	}
	wg.Wait()
	ck := c.OrderedKeys()
	report(okConc.Load() && slices.IsSorted(ck) && len(ck) == 32*50, "并发插入/查找一致")

	if failed {
		os.Exit(1)
	}
}
