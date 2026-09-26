package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/fnv"
	"ontology/tree"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

func leaves8() [][]byte {
	l := make([][]byte, 8)
	for i := range l {
		l[i] = []byte(fmt.Sprint(i + 1))
	}
	return l
}

func main() {
	// fnv：八个叶子哈希与 NOTES.md 推导一致。
	want := []uint32{0x2076AF6A, 0x1F76ADD7, 0x1E76AC44, 0x1D76AAB1,
		0x1C76A91E, 0x1B76A78B, 0x1A76A5F8, 0x1976A465}
	ok := true
	for i, w := range want {
		ok = ok && fnv.LeafHash([]byte(fmt.Sprint(i+1))) == w
	}
	check("fnv: 8 leaf hashes match derivation", ok)

	// tree：根与朴素参照（直接逐层 combine）逐字节一致，且等于推导值。
	tr, _ := tree.Build(leaves8())
	lv := make([]uint32, 8)
	for i, d := range leaves8() {
		lv[i] = fnv.LeafHash(d)
	}
	for len(lv) > 1 {
		nx := make([]uint32, len(lv)/2)
		for i := range nx {
			nx[i] = fnv.Combine(lv[2*i], lv[2*i+1])
		}
		lv = nx
	}
	check("tree: root == naive reference == 0x0887B3C1", tr.Root() == lv[0] && tr.Root() == 0x0887B3C1)

	// api：叶 2 认证路径验证通过。
	a, _ := api.Build(leaves8())
	p2, e1 := a.Proof(2)
	v2, e2 := a.Verify(2, []byte("3"), p2)
	check("api: proof(2) of leaf \"3\" verifies", e1 == nil && e2 == nil && v2)

	// api：防篡改——改一个字节必被拒。
	bad := []byte("4")
	bad[0] ^= 0xFF
	vBad, eBad := a.Verify(2, bad, p2)
	check("api: tampered leaf rejected", !vBad && errors.Is(eBad, api.ErrRootMismatch))

	// api：四类故障各有可判定且互不相同的哨兵错误。
	rootBefore := a.Root()
	_, eEmpty := api.Build(nil)
	_, ePow := api.Build(leaves8()[:3])
	_, eIdx := a.Proof(8)
	_, eRoot := a.Verify(0, []byte("X"), p2)
	distinct := map[error]bool{eEmpty: true, ePow: true, eIdx: true, eRoot: true}
	check("api: 4 distinct decidable errors", errors.Is(eEmpty, api.ErrEmptyLeaves) &&
		errors.Is(ePow, api.ErrNotPowerOfTwo) && errors.Is(eIdx, api.ErrIndexOutOfRange) &&
		errors.Is(eRoot, api.ErrRootMismatch) && len(distinct) == 4)

	// api：被拒后状态不变，仍可正常使用。
	vAgain, _ := a.Verify(2, []byte("3"), p2)
	check("api: state unchanged after rejections", a.Root() == rootBefore && vAgain)

	// tree：大 m 下认证路径长度 = log2(m)，不随 m 线性增长。
	ok = true
	for m := 128; m <= 8192; m <<= 2 {
		big := make([][]byte, m)
		for i := range big {
			big[i] = []byte(fmt.Sprint(i))
		}
		bt, _ := tree.Build(big)
		bp, _ := bt.Proof(m / 3)
		depth := 0
		for x := m; x > 1; x >>= 1 {
			depth++
		}
		ok = ok && len(bp) == depth
	}
	check("tree: proof length stays O(log m) up to m=8192", ok)

	// api：N 个 goroutine 并发 Verify 正确/篡改路径，结果一致。
	var wg sync.WaitGroup
	res := make(chan bool, 64)
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			i := g % 8
			p, _ := a.Proof(i)
			good, _ := a.Verify(i, []byte(fmt.Sprint(i+1)), p)
			tam, _ := a.Verify(i, []byte("zz"), p)
			res <- good && !tam
		}(g)
	}
	wg.Wait()
	close(res)
	ok = true
	for r := range res {
		ok = ok && r
	}
	check("api: 32 goroutines concurrent verify consistent", ok)

	// api：内置自检核验四条不变量。
	check("api: SelfCheck", a.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
