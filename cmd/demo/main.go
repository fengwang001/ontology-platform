package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/fnv"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Println(map[bool]string{true: "OK  ", false: "FAIL "}[ok] + name)
}

func main() {
	leaves := make([][]byte, 8)
	for i := range leaves {
		leaves[i] = []byte(fmt.Sprint(i + 1))
	}
	wantLeaf := [8]uint32{0x2076AF6A, 0x1F76ADD7, 0x1E76AC44, 0x1D76AAB1,
		0x1C76A91E, 0x1B76A78B, 0x1A76A5F8, 0x1976A465}
	ok := true
	for i, w := range wantLeaf {
		ok = ok && fnv.Leaf(leaves[i]) == w
	}
	tr, err := api.Build(leaves)
	root := tr.Root()
	check("leaf hashes + root match NOTES.md (root=0x0887B3C1)",
		ok && err == nil && binary.BigEndian.Uint32(root[:]) == 0x0887B3C1)

	path, err := tr.Proof(2)
	ok, _ = tr.Verify(2, []byte("3"), path)
	check("proof of leaf 2 verifies", err == nil && ok)

	ok, err = tr.Verify(2, []byte("3!"), path)
	check("tampered leaf rejected (ErrRootMismatch)", !ok && errors.Is(err, api.ErrRootMismatch))

	lvl := make([]uint32, 8) // 朴素参照：从叶子哈希逐层 combine 到根
	for i, d := range leaves {
		lvl[i] = fnv.Leaf(d)
	}
	for len(lvl) > 1 {
		next := make([]uint32, len(lvl)/2)
		for i := range next {
			next[i] = fnv.Combine(lvl[2*i], lvl[2*i+1])
		}
		lvl = next
	}
	check("root equals naive reference", binary.BigEndian.Uint32(root[:]) == lvl[0])

	_, e1 := api.Build(nil)
	_, e2 := api.Build(leaves[:3])
	_, e3 := tr.Proof(8)
	_, e4 := tr.Verify(2, []byte("3!"), path)
	errs := []error{e1, e2, e3, e4}
	want := []error{api.ErrEmptyLeaves, api.ErrNotPowerOfTwo, api.ErrIndexOutOfRange, api.ErrRootMismatch}
	ok = true
	for i := range errs {
		ok = ok && errors.Is(errs[i], want[i])
		for j := range want {
			ok = ok && (i == j || !errors.Is(errs[i], want[j]))
		}
	}
	check("four fault injections give four distinct sentinel errors", ok)

	ok, _ = tr.Verify(2, []byte("3"), path)
	check("state unchanged and usable after rejections", ok && tr.Root() == root && tr.LeafCount() == 8)

	ok = true // 大 m 下认证路径长度（=Verify 的 combine 次数）为 log2(m)，不线性增长
	for k := 7; k <= 13; k++ {
		m := 1 << k
		big := make([][]byte, m)
		for i := range big {
			big[i] = []byte(fmt.Sprint(i))
		}
		bt, err := api.Build(big)
		p, err2 := bt.Proof(m / 3)
		vok, err3 := bt.Verify(m/3, big[m/3], p)
		ok = ok && err == nil && err2 == nil && err3 == nil && vok && len(p) == k
	}
	check("proof length (=combine count) stays log2(m) for m=128..8192", ok)

	var wg sync.WaitGroup // 并发一致：正确路径全过、篡改路径全拒
	bad := make(chan bool, 64)
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			i := g % 8
			p, err := tr.Proof(i)
			ok1, err1 := tr.Verify(i, leaves[i], p)
			ok2, err2 := tr.Verify(i, append(leaves[i], '!'), p)
			if err != nil || err1 != nil || !ok1 || !errors.Is(err2, api.ErrRootMismatch) || ok2 {
				bad <- true
			}
		}(g)
	}
	wg.Wait()
	check("64 goroutines verify concurrently, consistent", len(bad) == 0)

	check("SelfCheck passes", tr.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
